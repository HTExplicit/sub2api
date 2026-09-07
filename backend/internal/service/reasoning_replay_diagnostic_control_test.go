//go:build reasoning_fidelity && reasoning_replay_diagnostic

package service_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type replayDiagnosticOfflineUpstream struct {
	calls    int
	status   int
	response string
	last     *http.Request
	body     []byte
}

func (s *replayDiagnosticOfflineUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	s.last = req
	s.body, _ = io.ReadAll(req.Body)
	status := s.status
	if status == 0 {
		status = 200
	}
	response := s.response
	if response == "" {
		response = `{"id":"resp_private","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"{"count":25,"first":"ABCDFE","last":"CBEADF"}"}]}],"usage":{"input_tokens":1,"output_tokens":2}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}
func (s *replayDiagnosticOfflineUpstream) DoWithTLS(req *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, p, id, n)
}
func replayDiagnosticOfflineBudget(t *testing.T, caseID int, grants ...map[string]any) (*replayDiagnosticUpstream, *replayDiagnosticOfflineUpstream, *bytes.Buffer) {
	t.Helper()
	var input, output bytes.Buffer
	for _, grant := range grants {
		if err := json.NewEncoder(&input).Encode(grant); err != nil {
			t.Fatal(err)
		}
	}
	source := fidelityOfflineBootstrap().Source
	source.UserID = 920000015522
	h := &replayDiagnosticHarness{boot: replayDiagnosticBootstrap{SchemaVersion: 1, RunID: replayDiagnosticRunID, ConfigMode: "production_env", SourceSHA: strings.Repeat("a", 40), Source: source}, input: bufio.NewReader(&input), output: json.NewEncoder(&output), ctx: context.Background()}
	h.accountHash = fidelityHashJSON(&h.boot.Source.Account)
	fake := &replayDiagnosticOfflineUpstream{}
	fixture := "native_single"
	if caseID == 6 {
		fixture = "invalid_cipher"
	}
	u := &replayDiagnosticUpstream{h: h, inner: fake, fixture: replayDiagnosticCase{ID: caseID, Model: "gpt-5.6-sol", Effort: "xhigh", Fixture: fixture}, used: make(map[int]bool), endpoint: "https://upstream.invalid/v1/responses", authorizationHash: fidelityHash([]byte("Bearer offline-fake"))}
	h.upstream = u
	return u, fake, &output
}
func replayDiagnosticGrant(caseID, slot, attempt int) map[string]any {
	return map[string]any{"type": "send_granted", "case_id": caseID, "slot": slot, "attempt": attempt, "fingerprint": strings.Repeat("a", 64), "elapsed_ms": 0}
}
func replayDiagnosticReadClose(t *testing.T, resp *http.Response, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	if err = resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestReasoningReplayDiagnosticControl(t *testing.T) {
	t.Run("one_send_and_private_attempt_evidence", func(t *testing.T) {
		u, fake, out := replayDiagnosticOfflineBudget(t, 1, replayDiagnosticGrant(1, 1, 1))
		resp, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		replayDiagnosticReadClose(t, resp, err)
		if fake.calls != 1 || u.attempts != 1 || fake.last.GetBody != nil || !service.HTTPUpstreamRedirectsDisabled(fake.last.Context()) {
			t.Fatal("outbound boundary not guarded")
		}
		if _, err = u.Do(fidelityOfflineRequest(t), "", 15522, 1); err == nil || fake.calls != 1 || u.blocked != 1 {
			t.Fatal("ordinary internal retry escaped")
		}
		if strings.Count(out.String(), `"type":"before_send"`) != 1 || strings.Count(out.String(), `"type":"attempt_result"`) != 1 {
			t.Fatal("attempt evidence protocol incomplete")
		}
		for _, private := range []string{"offline-fake", "upstream.invalid", "resp_private", "ABCDFE"} {
			if strings.Contains(out.String(), private) {
				t.Fatal("raw source or answer leaked")
			}
		}
	})
	t.Run("exact_rejection_allows_one_extra_and_not_third", func(t *testing.T) {
		u, fake, out := replayDiagnosticOfflineBudget(t, 6, replayDiagnosticGrant(6, 6, 1), replayDiagnosticGrant(6, 7, 2))
		fake.status = 400
		fake.response = `{"error":{"code":"invalid_encrypted_content","message":"never expose this upstream body"}}`
		resp, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		replayDiagnosticReadClose(t, resp, err)
		fake.status = 200
		fake.response = ""
		resp, err = u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		replayDiagnosticReadClose(t, resp, err)
		if fake.calls != 2 || u.attempts != 2 || len(u.records) != 2 || u.records[0].result.ErrorCode != "invalid_encrypted_content" {
			t.Fatal("second recovery attempt not distinct")
		}
		if _, err = u.Do(fidelityOfflineRequest(t), "", 15522, 1); err == nil || fake.calls != 2 {
			t.Fatal("third call escaped")
		}
		if strings.Contains(out.String(), "never expose") {
			t.Fatal("private upstream error leaked")
		}
	})
	t.Run("keywords_generic_error_and_failed_code_cannot_recover", func(t *testing.T) {
		for _, response := range []string{`{"error":{"message":"invalid_encrypted_content"}}`, `{"error":{"code":"upstream_error"}}`, `{"error":{"code":400}}`} {
			u, fake, _ := replayDiagnosticOfflineBudget(t, 6, replayDiagnosticGrant(6, 6, 1), replayDiagnosticGrant(6, 7, 2))
			fake.status = 400
			fake.response = response
			resp, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
			replayDiagnosticReadClose(t, resp, err)
			if _, err = u.Do(fidelityOfflineRequest(t), "", 15522, 1); err == nil || fake.calls != 1 {
				t.Fatal("unproven rejection retried")
			}
		}
	})
	t.Run("source_auth_budget_and_cancel_fail_before_grant", func(t *testing.T) {
		for _, kind := range []string{"source", "budget", "cancel", "auth"} {
			u, fake, out := replayDiagnosticOfflineBudget(t, 1, replayDiagnosticGrant(1, 1, 1))
			req := fidelityOfflineRequest(t)
			switch kind {
			case "source":
				u.h.boot.Source.Account.Credentials["api_key"] = "changed"
			case "budget":
				u.attempts = 16
			case "cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				u.activeContext = ctx
			case "auth":
				req.Header.Set("Authorization", "Bearer different")
			}
			if _, err := u.Do(req, "", 15522, 1); err == nil || fake.calls != 0 || out.Len() != 0 {
				t.Fatalf("unapproved %s send escaped", kind)
			}
		}
	})
	t.Run("auth_quota_stops_all_subsequent_calls", func(t *testing.T) {
		for _, status := range []int{401, 403, 402, 429} {
			u, fake, _ := replayDiagnosticOfflineBudget(t, 6, replayDiagnosticGrant(6, 6, 1), replayDiagnosticGrant(6, 7, 2))
			fake.status = status
			fake.response = `{"error":{"code":"invalid_encrypted_content"}}`
			resp, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
			replayDiagnosticReadClose(t, resp, err)
			if _, err = u.Do(fidelityOfflineRequest(t), "", 15522, 1); err == nil || fake.calls != 1 || u.h.stopped == "" {
				t.Fatal("auth or quota did not stop recovery")
			}
		}
	})
	t.Run("recoveries_share_the_original_deadline", func(t *testing.T) {
		u, fake, _ := replayDiagnosticOfflineBudget(t, 6, replayDiagnosticGrant(6, 6, 1), replayDiagnosticGrant(6, 7, 2))
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		u.activeContext = ctx
		fake.status = 400
		fake.response = `{"error":{"code":"thinking_signature_invalid"}}`
		resp, err := u.Do(fidelityOfflineRequest(t).WithContext(context.WithoutCancel(ctx)), "", 15522, 1)
		replayDiagnosticReadClose(t, resp, err)
		deadline1, ok := fake.last.Context().Deadline()
		if !ok {
			t.Fatal("detached Forward lost study deadline")
		}
		fake.status = 200
		fake.response = ""
		resp, err = u.Do(fidelityOfflineRequest(t).WithContext(context.WithoutCancel(ctx)), "", 15522, 1)
		replayDiagnosticReadClose(t, resp, err)
		deadline2, _ := fake.last.Context().Deadline()
		if !deadline1.Equal(deadline2) {
			t.Fatal("recovery extended the request deadline")
		}
	})
	t.Run("bad_grant_is_zero_network_calls", func(t *testing.T) {
		for _, bad := range []map[string]any{{"type": "send_denied"}, replayDiagnosticGrant(6, 7, 1), replayDiagnosticGrant(1, 1, 2)} {
			u, fake, _ := replayDiagnosticOfflineBudget(t, 1, bad)
			if _, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1); err == nil || fake.calls != 0 || u.h.stopped == "" {
				t.Fatal("invalid permission sent")
			}
		}
	})
	t.Run("controlled_cipher_changes_no_other_raw_state", func(t *testing.T) {
		body := fidelityRequestBody("gpt-5.6-sol", "xhigh", fidelitySinglePrompt, false)
		items := []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"rs_private","encrypted_content":"opaque-long-private-cipher","summary":[{"type":"summary_text","text":"visible"}],"extension":{"keep":true}}`), json.RawMessage(`{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"previous answer"}],"extension":true}`)}
		e := replayDiagnosticExchange{request: body, result: replayDiagnosticResult{Status: "completed"}, raw: fidelityResponse{Status: "completed", ReasoningComplete: true, Output: items}}
		bad, cipher, ok := replayDiagnosticInvalidBody(e)
		if !ok || cipher == "opaque-long-private-cipher" {
			t.Fatal("controlled corrupt fixture unavailable")
		}
		var obj map[string]json.RawMessage
		var input []json.RawMessage
		_ = json.Unmarshal(bad, &obj)
		_ = json.Unmarshal(obj["input"], &input)
		if len(input) != 4 || !fidelityJSONEqual(input[2], items[1]) {
			t.Fatal("non-reasoning state changed")
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(items[0], &before)
		_ = json.Unmarshal(input[1], &after)
		delete(before, "encrypted_content")
		delete(after, "encrypted_content")
		if fidelityHashJSON(before) != fidelityHashJSON(after) {
			t.Fatal("cipher corruption altered another reasoning field")
		}
		e.raw.Output = append(e.raw.Output, items[0])
		if _, _, ok = replayDiagnosticInvalidBody(e); ok {
			t.Fatal("duplicate reasoning identity accepted")
		}
		e.raw.Output[2] = json.RawMessage(strings.Replace(string(items[0]), "rs_private", "rs_private_second", 1))
		multiple, _, ok := replayDiagnosticInvalidBody(e)
		if !ok {
			t.Fatal("distinct completed reasoning history rejected")
		}
		_ = json.Unmarshal(multiple, &obj)
		_ = json.Unmarshal(obj["input"], &input)
		if !fidelityJSONEqual(input[3], e.raw.Output[2]) {
			t.Fatal("fixture altered more than the first reasoning cipher")
		}
	})
	t.Run("negative_fixture_preserves_bad_old_and_new_reasoning_for_real_store", func(t *testing.T) {
		invalid := []byte(`{"model":"gpt-5.6-sol","reasoning":{"effort":"xhigh"},"input":[{"type":"reasoning","encrypted_content":"old-bad","summary":[]}]}`)
		recovered := replayDiagnosticExchange{result: replayDiagnosticResult{Status: "completed", RecoveryObserved: true}, raw: fidelityResponse{Status: "completed", Output: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"new-good","summary":[]}`), json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}`)}}}
		next, ciphers, ok := replayDiagnosticNegativeBody(invalid, recovered)
		if !ok || len(ciphers) != 1 {
			t.Fatal("negative followup unavailable")
		}
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(next, &obj)
		if !replayDiagnosticHasCipher(obj["input"], "old-bad") || !replayDiagnosticHasCipher(obj["input"], "new-good") {
			t.Fatal("harness pre-removed history instead of testing service")
		}
		recovered.result.RecoveryObserved = false
		if _, _, ok = replayDiagnosticNegativeBody(invalid, recovered); ok {
			t.Fatal("unproven recovery used for negative cache claim")
		}
	})
	t.Run("bootstrap_exact_identity_and_nonzero_synthetic_tenant", func(t *testing.T) {
		u, _, _ := replayDiagnosticOfflineBudget(t, 1)
		if err := replayDiagnosticValidateBootstrap(u.h.boot); err != nil {
			t.Fatal(err)
		}
		u.h.boot.Source.Account.ID = 15523
		if replayDiagnosticValidateBootstrap(u.h.boot) == nil {
			t.Fatal("different account accepted")
		}
	})
}
