package service

import (
	"context"
	"encoding/json"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PluginExtensionStateStore interface {
	DueExtensionStates(context.Context, string, extensionv1.DueStateRequest) ([]extensionv1.DueState, error)
	ReadExtensionState(context.Context, string, extensionv1.StateRequest) (extensionv1.StateResult, error)
	CompareSwapExtensionState(context.Context, string, extensionv1.StateRequest) (extensionv1.StateResult, error)
	AcquireExtensionLease(context.Context, string, extensionv1.LeaseRequest) (extensionv1.LeaseResult, error)
	ReleaseExtensionLease(context.Context, string, extensionv1.LeaseRequest) (extensionv1.LeaseResult, error)
}

type PluginExtensionAccountDirectory interface {
	ReadExtensionAccount(context.Context, int64) (*extensionv1.Account, error)
	ListExtensionAccounts(context.Context, extensionv1.AccountQuery) ([]extensionv1.Account, error)
	ResolveExtensionIdentity(context.Context, extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error)
}

// The owning plugin comes from the authenticated broker connection. No payload
// field can select another plugin namespace or a database table/SQL statement.
type pluginExtensionHost struct {
	active       func() bool
	allows       func(string, string, string, int64) bool
	accountScope func(context.Context) (*PluginInstallation, error)
	traffic      AccountTrafficObserveCache
	activity     *QuotaActivityService
	key          string
	state        PluginExtensionStateStore
	directory    PluginExtensionAccountDirectory
	installation *PluginInstallation
}

func (h *pluginExtensionHost) Call(ctx context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	if h.installation != nil && h.installation.RuntimeGeneration > 0 {
		ctx = WithPluginExecution(ctx, h.installation)
	}
	if (in.Operation == extensionv1.HostResolveIdentity || in.Operation == extensionv1.HostLeaseAcquire || in.Operation == extensionv1.HostStateDue || in.Operation == extensionv1.HostJobSubmit || in.Operation == extensionv1.HostMetricsQuery) && h.active != nil && !h.active() {
		return extensionv1.Result{}, status.Error(codes.Unavailable, "plugin capability is no longer active")
	}
	if h.state == nil && in.Operation != extensionv1.HostMetricsQuery {
		return extensionv1.Result{}, status.Error(codes.Unavailable, "extension state unavailable")
	}
	// One persisted snapshot per account-bearing call keeps list filtering
	// bounded and applies the same live bindings/rollout to every returned row.
	// State/lease cleanup and finishing a previously issued observation retain
	// their existing ownership/generation checks rather than creating new work.
	if h.accountScope != nil && pluginHostOperationUsesAccountScope(in.Operation) {
		current, scopeErr := h.accountScope(ctx)
		if scopeErr != nil || current == nil {
			return extensionv1.Result{}, status.Error(codes.PermissionDenied, "plugin account scope is no longer active")
		}
		scoped := *h
		scoped.installation = current
		previousAllows := h.allows
		scoped.allows = func(capability, platform, accountType string, accountID int64) bool {
			return (previousAllows == nil || previousAllows(capability, platform, accountType, accountID)) &&
				pluginHasInvocationCapability(current, extensionv1.Invocation{Capability: capability, AccountID: accountID}, platform, accountType)
		}
		h = &scoped
	}
	var value any
	var err error
	switch in.Operation {
	case extensionv1.HostMetricsQuery:
		var request extensionv1.AccountQuery
		if json.Unmarshal(in.Payload, &request) != nil || request.AccountID <= 0 {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid metrics query")
		}
		if h.directory == nil || h.traffic == nil {
			return extensionv1.Result{}, status.Error(codes.Unavailable, "account metrics unavailable")
		}
		account, queryErr := h.directory.ReadExtensionAccount(ctx, request.AccountID)
		if queryErr != nil || account == nil || account.ID != request.AccountID {
			return extensionv1.Result{}, status.Error(codes.NotFound, "account unavailable")
		}
		if !h.permitsCapability(extensionv1.CapabilityObservability, account.Platform, account.Type, account.ID) {
			return extensionv1.Result{}, status.Error(codes.PermissionDenied, "metrics are outside plugin capability")
		}
		counters, queryErr := h.traffic.Snapshot(ctx, request.AccountID)
		if queryErr != nil {
			return extensionv1.Result{}, status.Error(codes.Unavailable, "account metrics unavailable")
		}
		snapshot := extensionv1.AccountTrafficSnapshot{AccountID: account.ID, Protocols: map[string]extensionv1.AccountTrafficCounters{}}
		for protocol, counter := range counters {
			if protocol.Valid() {
				snapshot.Protocols[string(protocol)] = counter
			}
		}
		value = snapshot
	case extensionv1.HostStateDue:
		var req extensionv1.DueStateRequest
		if json.Unmarshal(in.Payload, &req) != nil || validatePluginKVNamespace(req.Namespace) != nil || req.Limit < 1 || req.Limit > 100 {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid due-state query")
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
			if in.Operation == extensionv1.HostAccountRead {
				value = account
			} else if in.Operation == extensionv1.HostFinishObservation {
				err = h.activity.FinishUnbilled(ctx, req.AccountID, h.key, req.ObservationID)
				value = map[string]bool{"finished": err == nil}
			} else {
				identity, resolveErr := h.directory.ResolveExtensionIdentity(ctx, req)
				err = resolveErr
				if err == nil && identity != nil && req.PrepareCredentials {
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

func pluginHostOperationUsesAccountScope(operation extensionv1.HostOperation) bool {
	switch operation {
	case extensionv1.HostAccountRead, extensionv1.HostAccountList, extensionv1.HostResolveIdentity,
		extensionv1.HostMetricsQuery, extensionv1.HostStateCompareSwap:
		return true
	default:
		return false
	}
}

func (h *pluginExtensionHost) permitsAccount(platform, accountType string, accountID int64, credentials bool) bool {
	if h.installation == nil || accountID <= 0 {
		return false
	}
	for _, capability := range h.installation.Manifest.Capabilities {
		if !pluginScopeMatches(capability.Platform, platform) || !pluginScopeMatches(capability.AccountType, accountType) {
			continue
		}
		if credentials && capability.ID != extensionv1.CapabilityCredentials {
			continue
		}
		if !credentials && capability.ID == extensionv1.CapabilityUI {
			continue
		}
		if h.allows != nil && !h.allows(capability.ID, platform, accountType, accountID) {
			continue
		}
		return true
	}
	return false
}

func (h *pluginExtensionHost) permitsCapability(id, platform, accountType string, accountID int64) bool {
	if h.installation == nil || accountID <= 0 {
		return false
	}
	for _, capability := range h.installation.Manifest.Capabilities {
		if capability.ID == id && pluginScopeMatches(capability.Platform, platform) && pluginScopeMatches(capability.AccountType, accountType) && (h.allows == nil || h.allows(id, platform, accountType, accountID)) {
			return true
		}
	}
	return false
}
