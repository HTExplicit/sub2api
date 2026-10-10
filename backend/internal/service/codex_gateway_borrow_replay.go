package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// Administrator-only replay shares the real sender, account allowlist and
// observation isolation. The caller executes local fixture tools and supplies
// complete history. It cannot inject credentials, cookies, destinations, or
// anchors belonging to a real user conversation.
type CodexBorrowReplay struct {
	Body    json.RawMessage   `json:"body"`
	Headers map[string]string `json:"headers"`
}

var codexBorrowReplayHeaders = map[string]bool{
	"user-agent": true, "originator": true, "version": true, "session-id": true, "thread-id": true,
	"x-client-request-id": true, "x-codex-window-id": true, "x-codex-installation-id": true,
	"x-codex-turn-metadata": true, "x-codex-turn-state": true,
}

func validateCodexBorrowReplay(request CodexBorrowDiagnosticRequest) error {
	if request.Scenario != "codex_replay" {
		if request.Replay != nil {
			return errors.New("replay requires codex_replay scenario")
		}
		return nil
	}
	if request.Transport != "http" || (request.Mode != "ordinary" && request.Mode != "borrowed") || request.Replay == nil {
		return errors.New("codex_replay requires HTTP, explicit mode and replay")
	}
	raw := request.Replay.Body
	if len(raw) == 0 || len(raw) > 1<<20 || !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return errors.New("replay body must be a JSON object up to 1 MiB")
	}
	body := gjson.ParseBytes(raw)
	if body.Get("model").String() != request.Model || body.Get("stream").Type != gjson.True || body.Get("store").Type != gjson.False || body.Get("previous_response_id").Exists() {
		return errors.New("replay requires the selected model, streaming, store=false and complete history")
	}
	session := body.Get("prompt_cache_key").String()
	if !strings.HasPrefix(session, "codex-acceptance-") || len(session) > 128 {
		return errors.New("replay requires an isolated codex-acceptance session")
	}
	if request.ServiceTier != "" && body.Get("service_tier").String() != request.ServiceTier {
		return errors.New("replay service tier must match its body")
	}
	seen := map[string]bool{}
	for name, value := range request.Replay.Headers {
		lower := strings.ToLower(name)
		if !codexBorrowReplayHeaders[lower] || seen[lower] || strings.ContainsAny(value, "\r\n") || len(value) > 16384 {
			return errors.New("unsupported or ambiguous replay header")
		}
		seen[lower] = true
		if (lower == "session-id" || lower == "thread-id") && value != session {
			return errors.New("replay session headers must match its isolated session")
		}
	}
	if !seen["session-id"] || !seen["thread-id"] {
		return errors.New("replay requires isolated session and thread headers")
	}
	// Reject trailing values and duplicate top-level keys, so validation and the
	// gateway's JSON reader cannot select different models or session identifiers.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	keys := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || keys[key] {
			return errors.New("duplicate replay body key")
		}
		keys[key] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
	}
	return nil
}

func (r *CodexBorrowReplay) applyHeaders(headers http.Header) {
	for name, value := range r.Headers {
		headers.Set(name, value)
	}
}
