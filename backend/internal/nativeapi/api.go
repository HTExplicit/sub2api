// Package nativeapi contains the shared payloads of the in-process domains.
// Stored JSON field names remain compatible with existing account data and ledgers.
package nativeapi

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	// Preserve the existing bound on internal policy plans and catalog payloads.
	MaxPayloadBytes         = 4 * 1024 * 1024
	Version                 = 1
	CapabilityProvider      = "extensions.provider.v1"
	CapabilityCatalog       = "extensions.catalog.v1"
	CapabilityRequest       = "extensions.request.v1"
	CapabilityAdmin         = "extensions.admin.v1"
	CapabilityUI            = "extensions.ui.v1"
	CapabilityCredentials   = "extensions.credentials.v1"
	CapabilityObservability = "extensions.observability.v1"
	CapabilityRecovery      = "extensions.recovery.v1"
)

var capabilities = map[string]bool{
	CapabilityProvider: true, CapabilityCatalog: true, CapabilityRequest: true,
	CapabilityAdmin: true, CapabilityUI: true,
	CapabilityCredentials:   true,
	CapabilityObservability: true,
	CapabilityRecovery:      true,
}

func IsCapability(id string) bool { return capabilities[id] }

type Dependency struct {
	Capability  string `json:"capability"`
	Platform    string `json:"platform,omitempty"`
	AccountType string `json:"account_type,omitempty"`
}

type Contribution struct {
	AccountEdit      *AccountEditDefinitionV1   `json:"account_edit,omitempty"`
	AccountCreate    *AccountCreateDefinitionV1 `json:"account_create,omitempty"`
	AccountView      *AccountViewDefinitionV1   `json:"account_view,omitempty"`
	ValueBindings    map[string]string          `json:"value_bindings,omitempty"`
	ResourceAction   *AccountResourceActionV1   `json:"resource_action,omitempty"`
	AllAccounts      bool                       `json:"all_accounts,omitempty"`
	RetainedControls bool                       `json:"retained_controls,omitempty"`
	Events           []string                   `json:"events,omitempty"`
	Capability       string                     `json:"capability,omitempty"`
	Assets           []string                   `json:"assets,omitempty"`
	ConfigFlag       string                     `json:"config_flag,omitempty"`
	Fields           []FormField                `json:"fields,omitempty"`
	DisplayFields    []DisplayField             `json:"display_fields,omitempty"`
	AccountFilter    *AccountFilter             `json:"account_filter,omitempty"`
	ID               string                     `json:"id"`
	Slot             string                     `json:"slot"`
	Label            map[string]string          `json:"label"`
	Action           string                     `json:"action,omitempty"`
	Entrypoint       string                     `json:"entrypoint,omitempty"`
	Permission       string                     `json:"permission"`
	Order            int                        `json:"order,omitempty"`
}

type DisplayValue struct {
	Label map[string]string `json:"label"`
	Tone  string            `json:"tone,omitempty"`
}

// DisplayField only reads scalar values explicitly supplied by the host mount.
// It cannot evaluate expressions or traverse arbitrary account properties.
type DisplayField struct {
	Key     string                  `json:"key"`
	Kind    string                  `json:"kind"`
	Prefix  string                  `json:"prefix,omitempty"`
	NewLine bool                    `json:"new_line,omitempty"`
	Values  map[string]DisplayValue `json:"values,omitempty"`
}

type FormField struct {
	Key           string            `json:"key"`
	Kind          string            `json:"kind"`
	Label         map[string]string `json:"label"`
	OptionsSource string            `json:"options_source,omitempty"`
	DefaultLabel  map[string]string `json:"default_label,omitempty"`
	DefaultSource string            `json:"default_source,omitempty"`
	Placeholder   string            `json:"placeholder,omitempty"`
	Rows          int               `json:"rows,omitempty"`
	MaxLength     int               `json:"max_length,omitempty"`
	Hint          map[string]string `json:"hint,omitempty"`
	ResetLabel    map[string]string `json:"reset_label,omitempty"`
	LimitMessage  map[string]string `json:"limit_message,omitempty"`
}

type AccountFilter struct {
	Platforms      []string `json:"platforms,omitempty"`
	Types          []string `json:"types,omitempty"`
	Statuses       []string `json:"statuses,omitempty"`
	ExcludeShadows bool     `json:"exclude_shadows,omitempty"`
}

var slots = map[string]bool{
	AccountEditSlot:   true,
	AccountCreateSlot: true,
	AccountViewSlot:   true,
	"admin.page":      true, "admin.settings": true, "account.actions": true,
	"account.details": true, "account.columns": true, "account.test": true, "account.test.prompt": true,
	"group.actions": true, "group.details": true, "navigation": true,
	"theme": true, "usage.details": true,
	"surface": true,
}

func ValidSlot(slot string) bool { return slots[slot] }

// Invocation carries one declared capability and a domain operation. The host
// validates the capability binding before crossing the process boundary.
type Invocation struct {
	Capability string          `json:"capability"`
	Operation  string          `json:"operation"`
	Payload    json.RawMessage `json:"payload"`
	// AccountID is set from the host's authorized execution target, never from
	// a plugin UI payload. It also partitions cached policy results.
	AccountID int64 `json:"account_id,omitempty"`
}

type Result struct {
	PluginID   int64           `json:"plugin_id,omitempty"`
	HTTPStatus int             `json:"http_status,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Code       string          `json:"code,omitempty"`
	Message    string          `json:"message,omitempty"`
}

type JobTarget struct {
	AccountID int64           `json:"account_id"`
	Payload   json.RawMessage `json:"payload"`
	Label     string          `json:"label,omitempty"`
}

type StateRequest struct {
	Namespace        string          `json:"namespace"`
	Key              string          `json:"key"`
	ExpectedRevision int64           `json:"expected_revision"`
	Value            json.RawMessage `json:"value,omitempty"`
}

type StateResult struct {
	Found    bool            `json:"found"`
	Applied  bool            `json:"applied"`
	Revision int64           `json:"revision"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// Handler is implemented by a domain plugin, never by a host forwarding shim.
type Handler interface {
	Invoke(context.Context, Invocation) (Result, error)
}

type OperationInvoker interface {
	InvokeOperation(context.Context, string, string, Invocation) (Result, error)
}

type CachedOperationInvoker interface {
	InvokeCachedOperation(context.Context, string, string, Invocation) (Result, error)
}

func Encode(value any) (json.RawMessage, error) { return json.Marshal(value) }

func Decode(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return errors.New("empty extension payload")
	}
	return json.Unmarshal(raw, into)
}
