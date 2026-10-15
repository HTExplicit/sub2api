package service

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// The embedded object is authoritative. These are compatibility projections,
// not additional identity owners. Unknown embedded fields stay in the body.
var codexMetadataFlatFields = map[string]string{
	"x-codex-installation-id": "installation_id", "installation_id": "installation_id",
	"session_id": "session_id", "thread_id": "thread_id", "x-codex-window-id": "window_id",
	"turn_id": "turn_id", "parent_thread_id": "parent_thread_id",
	"x-codex-parent-thread-id": "parent_thread_id", "parent_turn_id": "parent_turn_id",
	"root_turn_id": "root_turn_id", "forked_from_thread_id": "forked_from_thread_id",
}

func canonicalCodexMetadata(flat map[string]any) (map[string]any, []string, error) {
	canonical := map[string]any{}
	if value, exists := flat["x-codex-turn-metadata"]; exists {
		raw, ok := value.(string)
		if !ok || strings.TrimSpace(raw) == "" || decodeCodexMetadataObject([]byte(raw), &canonical) != nil || canonical == nil {
			return nil, nil, fmt.Errorf("client_metadata.x-codex-turn-metadata must contain a JSON object")
		}
	}
	conflicts := []string{}
	keys := make([]string, 0, len(codexMetadataFlatFields))
	for key := range codexMetadataFlatFields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		name := codexMetadataFlatFields[key]
		value, exists := flat[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, nil, fmt.Errorf("client_metadata.%s must be a string", key)
		}
		if original, exists := canonical[name]; exists {
			if original != text {
				conflicts = append(conflicts, key)
			}
		} else {
			canonical[name] = text
		}
	}
	for _, name := range []string{"installation_id", "session_id", "thread_id", "window_id", "turn_id", "parent_thread_id", "parent_turn_id", "root_turn_id", "forked_from_thread_id", "request_kind", "subagent_kind", "thread_source", "sandbox", "sandbox_mode"} {
		if value, exists := canonical[name]; exists {
			if _, ok := value.(string); !ok {
				return nil, nil, fmt.Errorf("Codex turn metadata %s must be a string", name)
			}
		}
	}
	for _, name := range []string{"installation_id", "session_id", "thread_id", "window_id", "parent_thread_id", "forked_from_thread_id"} {
		if value, ok := canonical[name].(string); ok {
			canonical[name] = strings.TrimSpace(value)
		}
	}
	return canonical, conflicts, nil
}

func normalizeCodexMetadataProjection(flat map[string]any) ([]string, error) {
	canonical, conflicts, err := canonicalCodexMetadata(flat)
	if err != nil {
		return nil, err
	}
	for key, name := range codexMetadataFlatFields {
		if value, exists := canonical[name]; exists {
			// Do not manufacture optional aliases that the caller did not send.
			if _, exists := flat[key]; exists || key == "session_id" || key == "thread_id" || key == "x-codex-window-id" {
				flat[key] = value
			}
		}
	}
	return conflicts, nil
}

func codexMetadataClientError(err error) error {
	return &UpstreamFailoverError{
		Scope: GatewayFailureScopeRequest, NextAccountAction: NextAccountStop,
		SuppressAccountHealthPenalty: true, ClientStatusCode: http.StatusBadRequest,
		ClientErrorType: "invalid_request_error", ClientErrorCode: "invalid_codex_client_metadata",
		ClientErrorParam: "client_metadata", ClientMessage: err.Error(),
	}
}

// headers contain a bounded compatibility projection. Large body-only fields
// (tool namespaces, attribution and workspace inventories) never enter headers.
var codexHeaderMetadataFields = []string{
	"installation_id", "session_id", "thread_id", "agent_name", "turn_id", "window_id", "window_number",
	"context_window_id", "request_kind", "history_initialization", "forked_from_thread_id",
	"forked_from_ordinal_exclusive", "parent_thread_id", "parent_turn_id", "root_turn_id", "subagent_kind",
	"thread_source", "turn_trigger", "sandbox", "sandbox_mode", "auto_review_enabled",
	"node_repl_auto_review_required", "node_repl_disabled", "turn_started_at_unix_ms", "analytics_enabled",
}

func (ids *codexFingerprintIDs) headerMetadata() map[string]any {
	metadata := map[string]any{}
	for _, key := range codexHeaderMetadataFields {
		if value, exists := ids.logicalMetadata[key]; exists {
			metadata[key] = value
		}
	}
	metadata["installation_id"] = ids.installationID
	if ids.sessionID != "" {
		metadata["session_id"] = ids.sessionID
	}
	if ids.threadID != "" {
		metadata["thread_id"] = ids.threadID
	}
	if ids.windowID != "" {
		metadata["window_id"] = ids.windowID
	}
	return metadata
}

func (ids *codexFingerprintIDs) captureCanonicalMetadata(flat map[string]any) {
	if ids.bodyIdentityCaptured {
		return
	}
	ids.bodyIdentityCaptured = true
	canonical, conflicts, err := canonicalCodexMetadata(flat)
	if err != nil {
		ids.err = codexMetadataClientError(err)
		return
	}
	merged := maps.Clone(ids.logicalMetadata)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range canonical {
		merged[key] = value
	}
	ids.logicalMetadata = merged
	ids.metadataConflicts = append(ids.metadataConflicts, conflicts...)
	if value := stringMetadataValue(canonical, "session_id"); value != "" {
		ids.sessionID = value
		if ids.synthetic && stringMetadataValue(canonical, "thread_id") == "" {
			ids.threadID = value
			ids.windowID = value + ":0"
		}
	} else if ids.synthetic && stringMetadataValue(canonical, "thread_id") != "" {
		ids.sessionID = stringMetadataValue(canonical, "thread_id")
	}

	ids.captureThreadAndWindow(stringMetadataValue(canonical, "thread_id"), stringMetadataValue(canonical, "window_id"))
}

func decodeCodexMetadataObject(raw []byte, target *map[string]any) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid Codex metadata JSON")
	}
	return decodeOpenAIJSONUseNumber(raw, target)
}

const codexMetadataConflictsContextKey = "codex_metadata_input_conflicts"

func recordCodexMetadataConflicts(c *gin.Context, conflicts []string) {
	if c != nil {
		c.Set(codexMetadataConflictsContextKey, slices.Clone(conflicts))
	}
	if len(conflicts) > 0 {
		slog.Debug("codex metadata canonical projection", "source", "body_embedded", "conflicting_fields", conflicts)
	}
}
