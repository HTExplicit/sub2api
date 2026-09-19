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
	key          string
	state        PluginExtensionStateStore
	directory    PluginExtensionAccountDirectory
	installation *PluginInstallation
}

func (h *pluginExtensionHost) Call(ctx context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	if h.state == nil {
		return extensionv1.Result{}, status.Error(codes.Unavailable, "extension state unavailable")
	}
	var value any
	var err error
	switch in.Operation {
	case extensionv1.HostStateDue:
		var req extensionv1.DueStateRequest
		if json.Unmarshal(in.Payload, &req) != nil || validatePluginKVNamespace(req.Namespace) != nil || req.Limit < 1 || req.Limit > 100 {
			return extensionv1.Result{}, status.Error(codes.InvalidArgument, "invalid due-state query")
		}
		value, err = h.state.DueExtensionStates(ctx, h.key, req)
	case extensionv1.HostAccountRead, extensionv1.HostAccountList, extensionv1.HostResolveIdentity:
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
				if h.permitsAccount(account.Platform, account.Type, false) {
					allowed = append(allowed, account)
				}
			}
			value = allowed
		} else {
			account, queryErr := h.directory.ReadExtensionAccount(ctx, req.AccountID)
			if queryErr != nil || account == nil {
				return extensionv1.Result{}, status.Error(codes.NotFound, "account unavailable")
			}
			if !h.permitsAccount(account.Platform, account.Type, in.Operation == extensionv1.HostResolveIdentity) {
				return extensionv1.Result{}, status.Error(codes.PermissionDenied, "account is outside plugin capability")
			}
			if in.Operation == extensionv1.HostAccountRead {
				value = account
			} else {
				value, err = h.directory.ResolveExtensionIdentity(ctx, req)
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

func (h *pluginExtensionHost) permitsAccount(platform, accountType string, credentials bool) bool {
	if h.installation == nil {
		return false
	}
	for _, capability := range h.installation.Manifest.Capabilities {
		if !pluginScopeMatches(capability.Platform, platform) || !pluginScopeMatches(capability.AccountType, accountType) {
			continue
		}
		if credentials && capability.ID != extensionv1.CapabilityProvider && capability.ID != extensionv1.CapabilityRequest {
			continue
		}
		if !credentials && capability.ID == extensionv1.CapabilityUI {
			continue
		}
		return true
	}
	return false
}
