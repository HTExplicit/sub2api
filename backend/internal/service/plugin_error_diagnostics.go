package service

import (
	"context"
	"encoding/json"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// PluginErrorDiagnostic contains only host-sanitized structural facts. Request
// bodies, headers, error messages, URLs and account names never enter the UI.
type PluginErrorDiagnostic struct {
	AccountID  int64                         `json:"account_id"`
	Attempt    int                           `json:"attempt"`
	Diagnostic *OpenAIContinuationDiagnostic `json:"diagnostic"`
}

func (m *PluginManager) ProjectErrorDiagnostics(ctx context.Context, detail *OpsErrorLogDetail) []PluginErrorDiagnostic {
	result := make([]PluginErrorDiagnostic, 0)
	if detail == nil || len(detail.UpstreamErrors) > 4<<20 {
		return result
	}
	var events []OpsUpstreamErrorEvent
	if json.Unmarshal([]byte(detail.UpstreamErrors), &events) != nil || len(events) > 128 {
		return result
	}
	for index, event := range events {
		if event.AccountID <= 0 || event.ContinuationDiagnostic == nil {
			continue
		}
		// Scope comes from the persisted event and current account, never the UI
		// payload. Deleted accounts remain visible in ordinary host history only.
		if m.ValidateResourceAccounts(ctx, extensionv1.CapabilityRecovery, []int64{event.AccountID}, false) != nil {
			continue
		}
		if safe := sanitizeOpenAIContinuationDiagnostic(event.ContinuationDiagnostic); safe != nil {
			result = append(result, PluginErrorDiagnostic{event.AccountID, index + 1, safe})
		}
	}
	return result
}
