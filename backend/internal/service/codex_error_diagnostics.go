package service

import (
	"context"
	"encoding/json"
)

// CodexErrorDiagnostic contains only host-sanitized structural facts. Request
// bodies, headers, error messages, URLs and account names never enter the UI.
type CodexErrorDiagnostic struct {
	AccountID  int64                         `json:"account_id"`
	Attempt    int                           `json:"attempt"`
	Diagnostic *OpenAIContinuationDiagnostic `json:"diagnostic"`
}

func ProjectCodexErrorDiagnostics(ctx context.Context, detail *OpsErrorLogDetail, readAccount func(context.Context, int64) (*Account, error)) []CodexErrorDiagnostic {
	result := make([]CodexErrorDiagnostic, 0)
	if detail == nil || readAccount == nil || len(detail.UpstreamErrors) > 4<<20 {
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
		account, err := readAccount(ctx, event.AccountID)
		if err != nil || account == nil || account.ID != event.AccountID || account.Platform != PlatformOpenAI {
			continue
		}
		if safe := sanitizeOpenAIContinuationDiagnostic(event.ContinuationDiagnostic); safe != nil {
			result = append(result, CodexErrorDiagnostic{event.AccountID, index + 1, safe})
		}
	}
	return result
}
