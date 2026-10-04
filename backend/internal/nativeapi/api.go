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
)

// Invocation carries one domain operation and its payload.
type Invocation struct {
	Operation string          `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}

type Result struct {
	HTTPStatus int             `json:"http_status,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Code       string          `json:"code,omitempty"`
	Message    string          `json:"message,omitempty"`
}

type OperationInvoker interface {
	InvokeOperation(context.Context, Invocation) (Result, error)
}
