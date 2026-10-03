// Package nativeapi contains the shared payloads of the in-process domains.
// Stored JSON field names remain compatible with existing account data.
package nativeapi

import (
	"context"
	"encoding/json"
)

const (
	// Preserve the existing bound on internal policy plans and catalog payloads.
	MaxPayloadBytes = 4 * 1024 * 1024
	CapabilityAdmin = "extensions.admin.v1"
)

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

type OperationInvoker interface {
	InvokeOperation(context.Context, Invocation) (Result, error)
}
