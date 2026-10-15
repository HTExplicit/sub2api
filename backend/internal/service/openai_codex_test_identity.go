package service

import (
	"encoding/json"
	"maps"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type codexSyntheticRoot struct {
	accountID int64
	threadID  string
	turnID    string
	startedMS int64
}

// Retries of one administrator test reuse one real synthetic root. No identifier
// depends on a deployment-relative account number.
func projectCodexAccountTest(c *gin.Context, routed, source *Account, body map[string]any) (http.Header, error) {
	const contextKey = "codex_synthetic_test_root"
	root := codexSyntheticRoot{}
	if value, exists := c.Get(contextKey); exists {
		root, _ = value.(codexSyntheticRoot)
	}
	if root.accountID != routed.ID || root.threadID == "" {
		thread, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		turn, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		root = codexSyntheticRoot{routed.ID, thread.String(), turn.String(), time.Now().UnixMilli()}
		c.Set(contextKey, root)
	}
	policy := codexFingerprintPolicyForContext(c, routed)
	metadata := map[string]any{
		"session_id": root.threadID, "thread_id": root.threadID, "window_id": root.threadID + ":0",
		"turn_id": root.turnID, "root_turn_id": root.turnID, "turn_started_at_unix_ms": root.startedMS,
		"request_kind": "turn", "agent_name": "/root", "window_number": 0,
	}
	encoded, err := marshalCodexTurnMetadata(metadata)
	if err != nil {
		return nil, err
	}
	flat := map[string]any{"session_id": root.threadID, "thread_id": root.threadID,
		"x-codex-window-id": root.threadID + ":0", "turn_id": root.turnID, "x-codex-turn-metadata": string(encoded)}
	body["client_metadata"] = flat
	body["prompt_cache_key"] = root.threadID
	account := *source
	account.Extra = maps.Clone(source.Extra)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[codexFingerprintModeExtraKey] = string(routed.GetCodexFingerprintMode())
	applyCodexAccountIdentityClientMetadataMap(body, &account, 0, policy)
	headers := http.Header{"Session-Id": {root.threadID}, "Thread-Id": {root.threadID},
		"X-Codex-Window-Id": {root.threadID + ":0"}, "X-Client-Request-Id": {root.threadID},
		"X-Codex-Turn-Metadata": {string(encoded)}}
	applyCodexAccountIdentityHeaders(headers, &account, 0, policy)
	ids := resolveCodexFingerprintIDsWithSource(routed, source, headers, policy)
	if ids != nil {
		applyCodexFingerprintClientMetadata(body, ids)
		if ids.err != nil {
			return nil, ids.err
		}
		applyCodexFingerprintHeaders(headers, ids)
	}
	return headers, nil
}

type CodexWireIdentity struct {
	InstallationID string   `json:"installation_id"`
	SessionID      string   `json:"session_id"`
	ThreadID       string   `json:"thread_id"`
	WindowID       string   `json:"window_id"`
	Consistent     bool     `json:"consistent"`
	Conflicts      []string `json:"conflicts"`
	InputConflicts []string `json:"input_conflicts,omitempty"`
}

func codexWireIdentity(body map[string]any, headers http.Header) *CodexWireIdentity {
	flat, ok := body["client_metadata"].(map[string]any)
	if !ok {
		return nil
	}
	metadata, conflicts, err := canonicalCodexMetadata(flat)
	if err != nil {
		return &CodexWireIdentity{Conflicts: []string{err.Error()}}
	}
	result := &CodexWireIdentity{InstallationID: stringMetadataValue(metadata, "installation_id"),
		SessionID: stringMetadataValue(metadata, "session_id"), ThreadID: stringMetadataValue(metadata, "thread_id"),
		WindowID: stringMetadataValue(metadata, "window_id"), Conflicts: conflicts}
	result.Consistent = len(conflicts) == 0 && result.SessionID == headers.Get("session-id") &&
		result.ThreadID == headers.Get("thread-id") && result.ThreadID == headers.Get("x-client-request-id") &&
		result.WindowID == headers.Get("x-codex-window-id")
	var headerMetadata map[string]any
	if json.Unmarshal([]byte(headers.Get("x-codex-turn-metadata")), &headerMetadata) == nil {
		result.Consistent = result.Consistent && result.InstallationID == stringMetadataValue(headerMetadata, "installation_id")
	}
	return result
}
