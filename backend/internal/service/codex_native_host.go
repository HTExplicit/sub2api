package service

import (
	"context"
	"encoding/json"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
)

type nativeCodexHost struct {
	leaseMu   sync.Mutex
	leaseWork sync.WaitGroup
	closing   bool
	key       string
	state     NativeCodexStateStore
	directory NativeCodexAccountDirectory
	metadata  *NativeCodexMetadata
	repo      NativeCodexRepository
	activity  *QuotaActivityService
	epoch     context.Context
}
type nativeCodexLeaseBindingKey struct{}
type nativeCodexPolicySignalsKey struct{}

func nativeCodexPolicySignals(ctx context.Context) []context.Context {
	signals, _ := ctx.Value(nativeCodexPolicySignalsKey{}).([]context.Context)
	return signals
}

type nativeCodexLeaseBinding struct {
	metadata NativeCodexMetadata
	ctx      context.Context
}

func (h *nativeCodexHost) bind(parent context.Context) (context.Context, context.CancelFunc, error) {
	if h == nil || h.metadata == nil || h.repo == nil || h.epoch == nil || h.epoch.Err() != nil {
		return nil, nil, ErrNativeCodexRuntimeUnavailable
	}
	if wanted, ok := NativeCodexExecutionFromContext(parent); ok && wanted != *h.metadata {
		return nil, nil, ErrNativeCodexRuntimeChanged
	}
	if existing, ok := parent.Value(nativeCodexLeaseBindingKey{}).(nativeCodexLeaseBinding); ok && existing.metadata == *h.metadata && existing.ctx.Err() == nil {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, nil
	}
	h.leaseMu.Lock()
	if h.closing {
		h.leaseMu.Unlock()
		return nil, nil, ErrNativeCodexRuntimeUnavailable
	}
	h.leaseWork.Add(1)
	h.leaseMu.Unlock()
	bound, cancel := context.WithCancel(WithNativeCodexExecution(parent, h.metadata))
	signal, stopSignal := context.WithCancel(context.Background())
	stopPropagation := context.AfterFunc(signal, cancel)
	stopEpoch := context.AfterFunc(h.epoch, stopSignal)
	signals := append(append([]context.Context(nil), nativeCodexPolicySignals(parent)...), signal)
	bound = context.WithValue(bound, nativeCodexPolicySignalsKey{}, signals)
	if h.epoch.Err() != nil {
		stopSignal()
		cancel()
	}
	lease, err := h.repo.HoldNativeCodexRuntime(WithNativeCodexBusinessIO(bound), h.metadata)
	if err != nil {
		stopEpoch()
		stopPropagation()
		stopSignal()
		cancel()
		h.leaseWork.Done()
		return nil, nil, err
	}
	if lease == nil || lease.Done() == nil {
		stopEpoch()
		stopPropagation()
		stopSignal()
		cancel()
		if lease != nil {
			lease.Release()
		}
		h.leaseWork.Done()
		return nil, nil, ErrNativeCodexRuntimeUnavailable
	}
	go func() {
		<-lease.Done()
		if lease.Err() != nil {
			stopSignal()
		}
	}()
	binding := nativeCodexLeaseBinding{metadata: *h.metadata, ctx: bound}
	bound = context.WithValue(bound, nativeCodexLeaseBindingKey{}, binding)
	var once sync.Once
	return bound, func() {
		once.Do(func() {
			stopEpoch()
			stopPropagation()
			stopSignal()
			cancel()
			lease.Release()
			h.leaseWork.Done()
		})
	}, nil
}

func (h *nativeCodexHost) drain(ctx context.Context) error {
	h.leaseMu.Lock()
	h.closing = true
	h.leaseMu.Unlock()
	done := make(chan struct{})
	go func() { h.leaseWork.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *nativeCodexHost) permitsAccount(platform, accountType string, accountID int64, credentials bool) bool {
	return accountID > 0 && platform == PlatformOpenAI && (!credentials || accountType == AccountTypeOAuth || accountType == AccountTypeSetupToken)
}

func (h *nativeCodexHost) Call(ctx context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	bound, release, err := h.bind(ctx)
	if err != nil {
		return extensionv1.Result{}, err
	}
	defer release()
	ctx = bound
	if h.state == nil {
		return extensionv1.Result{}, ErrNativeCodexRuntimeUnavailable
	}
	if isCodexRoutingHostOperation(in.Operation) {
		return h.callCodexRouting(ctx, in)
	}
	var value any
	switch in.Operation {
	case extensionv1.HostStateDue:
		var req extensionv1.DueStateRequest
		if json.Unmarshal(in.Payload, &req) != nil || validatePluginKVNamespace(req.Namespace) != nil || req.Limit < 1 || req.Limit > 100 {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid due-state query")
		}
		if req.Namespace == codexRoutingPrivateNamespace {
			return extensionv1.Result{}, status.Error(codes.PermissionDenied, "host-owned routing material is private")
		}
		value, err = h.state.DueExtensionStates(ctx, h.key, req)
	case extensionv1.HostAccountRead, extensionv1.HostAccountList, extensionv1.HostResolveIdentity, extensionv1.HostFinishObservation:
		if h.directory == nil {
			return extensionv1.Result{}, status.Error(codes.Unavailable, "account directory unavailable")
		}
		var req extensionv1.AccountQuery
		if json.Unmarshal(in.Payload, &req) != nil {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid account query")
		}
		if in.Operation == extensionv1.HostAccountList {
			if req.Platform == "" || req.Platform == "*" {
				return extensionv1.Result{}, status.Error(codes.InvalidArgument, "explicit platform required")
			}
			accounts, queryErr := h.directory.ListExtensionAccounts(ctx, req)
			if queryErr != nil {
				return extensionv1.Result{}, status.Error(codes.Internal, "account query failed")
			}
			allowed := make([]extensionv1.Account, 0, len(accounts))
			for _, account := range accounts {
				if h.permitsAccount(account.Platform, account.Type, account.ID, false) {
					allowed = append(allowed, account)
				}
			}
			value = allowed
		} else {
			if req.AccountID <= 0 {
				return extensionv1.Result{}, status.Error(codes.InvalidArgument, "positive account identifier required")
			}
			account, queryErr := h.directory.ReadExtensionAccount(ctx, req.AccountID)
			if queryErr != nil || account == nil || account.ID != req.AccountID {
				return extensionv1.Result{}, status.Error(codes.NotFound, "account unavailable")
			}
			if !h.permitsAccount(account.Platform, account.Type, account.ID, in.Operation == extensionv1.HostResolveIdentity) {
				return extensionv1.Result{}, status.Error(codes.PermissionDenied, "account is outside plugin capability")
			}
			switch in.Operation {
			case extensionv1.HostAccountRead:
				value = account
			case extensionv1.HostFinishObservation:
				if h.activity == nil {
					return extensionv1.Result{}, ErrNativeCodexRuntimeUnavailable
				}
				err = h.activity.FinishUnbilled(ctx, req.AccountID, h.key, req.ObservationID)
				value = map[string]bool{"finished": err == nil}
			default:
				identity, resolveErr := h.directory.ResolveExtensionIdentity(ctx, req)
				err = resolveErr
				if err == nil && identity != nil && req.PrepareCredentials {
					if h.activity == nil {
						return extensionv1.Result{}, ErrNativeCodexRuntimeUnavailable
					}
					identity.ObservationID, err = h.activity.BeginUnbilled(ctx, req.AccountID, h.key)
				}
				value = identity
			}
		}
	case extensionv1.HostStateRead, extensionv1.HostStateCompareSwap:
		var req extensionv1.StateRequest
		if json.Unmarshal(in.Payload, &req) != nil || validatePluginKVNamespace(req.Namespace) != nil || validatePluginKVKey(req.Key) != nil || req.ExpectedRevision < 0 {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid state request")
		}
		if req.Namespace == codexRoutingPrivateNamespace {
			return extensionv1.Result{}, status.Error(codes.PermissionDenied, "host-owned routing material is private")
		}
		if in.Operation == extensionv1.HostStateRead {
			value, err = h.state.ReadExtensionState(ctx, h.key, req)
		} else {
			if !json.Valid(req.Value) || len(req.Value) > pluginKVMaxValueBytes {
				return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid state value")
			}
			if projection := req.Projection; projection != nil {
				if h.directory == nil || projection.AccountID <= 0 || projection.Identity == "" || len(projection.Scheduling) > 64 {
					return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid account projection")
				}
				account, lookupErr := h.directory.ReadExtensionAccount(ctx, projection.AccountID)
				if lookupErr != nil || account == nil || account.ID != projection.AccountID || account.Identity != projection.Identity || !h.permitsAccount(account.Platform, account.Type, account.ID, false) {
					return extensionv1.Result{}, status.Error(codes.PermissionDenied, "account projection outside credential scope")
				}
				for _, constraint := range projection.Scheduling {
					if constraint.Model == "" || len(constraint.Model) > 256 || (constraint.Effect != "allow" && constraint.Effect != "deny") || len(constraint.Reason) > 80 {
						return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid scheduling projection")
					}
				}
				if len(projection.Observations) > 64 {
					return extensionv1.Result{}, status.Error(codes.InvalidArgument, "too many observations")
				}
				for _, observation := range projection.Observations {
					if observation.Key == "" || len(observation.Key) > 256 || len(observation.Kind) > 64 || len(observation.State) > 64 || len(observation.Code) > 80 {
						return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid account observation")
					}
				}
			}
			value, err = h.state.CompareSwapExtensionState(ctx, h.key, req)
		}
	case extensionv1.HostLeaseAcquire, extensionv1.HostLeaseRelease:
		var req extensionv1.LeaseRequest
		if json.Unmarshal(in.Payload, &req) != nil || validatePluginKVNamespace(req.Namespace) != nil || validatePluginKVKey(req.Key) != nil || validatePluginKVKey(req.Owner) != nil {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid lease request")
		}
		if in.Operation == extensionv1.HostLeaseAcquire {
			if req.TTLSeconds < 1 || req.TTLSeconds > 300 {
				return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid lease duration")
			}
			value, err = h.state.AcquireExtensionLease(ctx, h.key, req)
		} else {
			if req.Generation <= 0 {
				return extensionv1.Result{}, status.Error(codes.InvalidArgument, "lease generation required")
			}
			value, err = h.state.ReleaseExtensionLease(ctx, h.key, req)
		}
	default:
		return extensionv1.Result{}, status.Error(codes.PermissionDenied, "host operation not granted")
	}
	if err != nil {
		return extensionv1.Result{}, status.Error(codes.Internal, "extension storage operation failed")
	}
	raw, err := json.Marshal(value)
	return extensionv1.Result{Payload: raw}, err
}
