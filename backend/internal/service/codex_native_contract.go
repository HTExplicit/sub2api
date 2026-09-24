package service

import (
	"context"
	"encoding/json"
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

const NativeCodexPluginKey = "codexrip.codex-runtime"
const NativeCodexConfigSettingKey = "codex_native_runtime_config"
const NativeCodexSourceSettingKey = "codex_native_runtime_source"
const NativeCodexRetirementSettingKey = "deplugin_retired_plugins"
const NativeCodexAccountProjectionKey = "plugin_account_projections"

var ErrNativeCodexRuntimeChanged = errors.New("native Codex runtime metadata changed")
var ErrNativeCodexRuntimeUnavailable = errors.New("native Codex runtime unavailable")

// The disabled legacy row is a persistence anchor, never a running plugin.
type NativeCodexMetadata struct {
	ID                int64
	RuntimeGeneration int64
	ConfigVersion     int64
	ConfigSHA256      string
}

type NativeCodexConfigRecord struct {
	Version       int    `json:"version"`
	ConfigVersion int64  `json:"config_version"`
	ConfigSHA256  string `json:"config_sha256"`
}

type NativeCodexStateStore interface {
	DueExtensionStates(context.Context, string, extensionv1.DueStateRequest) ([]extensionv1.DueState, error)
	ReadExtensionState(context.Context, string, extensionv1.StateRequest) (extensionv1.StateResult, error)
	CompareSwapExtensionState(context.Context, string, extensionv1.StateRequest) (extensionv1.StateResult, error)
	AcquireExtensionLease(context.Context, string, extensionv1.LeaseRequest) (extensionv1.LeaseResult, error)
	ReleaseExtensionLease(context.Context, string, extensionv1.LeaseRequest) (extensionv1.LeaseResult, error)
}

type NativeCodexAccountDirectory interface {
	ReadExtensionAccount(context.Context, int64) (*extensionv1.Account, error)
	ListExtensionAccounts(context.Context, extensionv1.AccountQuery) ([]extensionv1.Account, error)
	ResolveExtensionIdentity(context.Context, extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error)
}

type NativeCodexRuntimeLease interface {
	Done() <-chan struct{}
	Err() error
	Release()
}

type NativeCodexRepository interface {
	NativeCodexStateStore
	ReadNativeCodexStoredConfig(context.Context) (string, bool, error)
	LoadNativeCodexMetadata(context.Context) (*NativeCodexMetadata, error)
	SyncNativeCodexConfig(context.Context, string) (*NativeCodexMetadata, error)
	StoreNativeCodexConfig(context.Context, string, string) (*NativeCodexMetadata, error)
	HoldNativeCodexRuntime(context.Context, *NativeCodexMetadata) (NativeCodexRuntimeLease, error)
}

type NativeCodexConfigFactory func(context.Context) (json.RawMessage, error)

type nativeCodexExecutionKey struct{}
type nativeCodexBusinessIOKey struct{}

func WithNativeCodexExecution(ctx context.Context, metadata *NativeCodexMetadata) context.Context {
	if metadata == nil {
		return ctx
	}
	return context.WithValue(ctx, nativeCodexExecutionKey{}, *metadata)
}

func NativeCodexExecutionFromContext(ctx context.Context) (NativeCodexMetadata, bool) {
	metadata, ok := ctx.Value(nativeCodexExecutionKey{}).(NativeCodexMetadata)
	return metadata, ok
}

func WithNativeCodexBusinessIO(ctx context.Context) context.Context {
	return context.WithValue(ctx, nativeCodexBusinessIOKey{}, true)
}

func NativeCodexBusinessIORequired(ctx context.Context) bool {
	required, _ := ctx.Value(nativeCodexBusinessIOKey{}).(bool)
	return required
}

type NativeCodexAccountProjection struct {
	Identity     string                                      `json:"identity"`
	Scheduling   map[string]extensionv1.SchedulingConstraint `json:"scheduling"`
	Observations map[string]extensionv1.AccountObservation   `json:"observations,omitempty"`
}

func nativeCodexAccountProjection(account *Account) NativeCodexAccountProjection {
	var projection NativeCodexAccountProjection
	if account != nil {
		if values, ok := account.Extra[NativeCodexAccountProjectionKey].(map[string]any); ok {
			if raw, err := json.Marshal(values[NativeCodexPluginKey]); err == nil {
				_ = json.Unmarshal(raw, &projection)
			}
		}
	}
	return projection
}
