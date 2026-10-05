package service

import (
	"encoding/json"
)

// CodexErrorDiagnostic projects one persisted upstream attempt that carries a
// continuation diagnostic, together with that attempt's own context. The
// administrator sees every stored record, including the bounded original values
// kept inside the diagnostic.
type CodexErrorDiagnostic struct {
	AccountID          int64                         `json:"account_id"`
	AccountName        string                        `json:"account_name,omitempty"`
	Platform           string                        `json:"platform,omitempty"`
	Attempt            int                           `json:"attempt"`
	Kind               string                        `json:"kind,omitempty"`
	UpstreamStatusCode int                           `json:"upstream_status_code,omitempty"`
	UpstreamRequestID  string                        `json:"upstream_request_id,omitempty"`
	Message            string                        `json:"message,omitempty"`
	AtUnixMs           int64                         `json:"at_unix_ms,omitempty"`
	Diagnostic         *OpenAIContinuationDiagnostic `json:"diagnostic"`
}

// ProjectCodexErrorDiagnostics returns every attempt diagnostic stored on one
// Ops error, each with its attempt's context.
func ProjectCodexErrorDiagnostics(detail *OpsErrorLogDetail) []CodexErrorDiagnostic {
	result := make([]CodexErrorDiagnostic, 0)
	if detail == nil {
		return result
	}
	var events []OpsUpstreamErrorEvent
	if json.Unmarshal([]byte(detail.UpstreamErrors), &events) != nil {
		return result
	}
	for index, event := range events {
		if event.ContinuationDiagnostic == nil {
			continue
		}
		diagnostic := sanitizeOpenAIContinuationDiagnostic(event.ContinuationDiagnostic)
		if diagnostic == nil {
			continue
		}
		result = append(result, CodexErrorDiagnostic{
			AccountID:          event.AccountID,
			AccountName:        event.AccountName,
			Platform:           event.Platform,
			Attempt:            index + 1,
			Kind:               event.Kind,
			UpstreamStatusCode: event.UpstreamStatusCode,
			UpstreamRequestID:  event.UpstreamRequestID,
			Message:            event.Message,
			AtUnixMs:           event.AtUnixMs,
			Diagnostic:         diagnostic,
		})
	}
	return result
}
