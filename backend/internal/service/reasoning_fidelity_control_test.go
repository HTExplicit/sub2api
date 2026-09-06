//go:build reasoning_fidelity

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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type fidelityOfflineUpstream struct {
	calls int
	last  *http.Request
	body  []byte
}

func (f *fidelityOfflineUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	f.calls++
	f.last = req
	f.body, _ = io.ReadAll(req.Body)
	response := `{"id":"resp_offline","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"{\"count\":25,\"first\":\"ABCDFE\",\"last\":\"CBEADF\"}"}]}],"usage":{"input_tokens":1,"output_tokens":2}}`
	contentType := "application/json"
	var request struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(f.body, &request)
	if request.Stream {
		contentType = "text/event-stream"
		response = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\ndata: [DONE]\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}
func (f *fidelityOfflineUpstream) DoWithTLS(req *http.Request, p string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(req, p, id, c)
}

func fidelityOfflineBootstrap() fidelityBootstrap {
	return fidelityBootstrap{SchemaVersion: 1, Phase: "before", RunID: "offline", ConfigMode: "production_env", Source: fidelitySource{
		Account: service.Account{ID: 15522, Name: "白嫖666", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"api_key": "offline-fake", "base_url": "https://upstream.invalid/v1"}, Extra: map[string]any{"openai_responses_supported": true, "openai_responses_mode": "responses"}, GroupIDs: []int64{4}},
		Group: service.Group{ID: 4, Platform: service.PlatformOpenAI, Status: service.StatusActive}, FastPolicy: service.DefaultOpenAIFastPolicySettings(), Settings: map[string]string{}, ChannelModels: map[string]string{"gpt-5.6-sol": "gpt-5.6-sol", "gpt-6-astra": "gpt-6-astra"}, Fingerprint: strings.Repeat("a", 64)}}
}

func fidelityOfflineBudget(t *testing.T, grant fidelityGrant) (*fidelityBudgetUpstream, *fidelityOfflineUpstream, *bytes.Buffer) {
	t.Helper()
	var input, output bytes.Buffer
	if err := json.NewEncoder(&input).Encode(grant); err != nil {
		t.Fatal(err)
	}
	h := &fidelityHarness{boot: fidelityOfflineBootstrap(), input: bufio.NewReader(&input), output: json.NewEncoder(&output), ctx: context.Background()}
	h.sourceAccountSHA = fidelityHashJSON(&h.boot.Source.Account)
	fake := &fidelityOfflineUpstream{}
	u := &fidelityBudgetUpstream{h: h, inner: fake, used: map[int]bool{}, activeSlot: 1, endpoint: "https://upstream.invalid/v1/responses", authorizationSHA: fidelityHash([]byte("Bearer offline-fake"))}
	h.upstream = u
	return u, fake, &output
}

func fidelityOfflineRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, "https://upstream.invalid/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","input":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer offline-fake")
	return r
}

func TestReasoningFidelityControl(t *testing.T) {
	t.Run("budget_grant_is_single_send_including_tls_retry", func(t *testing.T) {
		u, fake, out := fidelityOfflineBudget(t, fidelityGrant{Type: "send_granted", Slot: 1, Fingerprint: strings.Repeat("a", 64), Attempt: 1})
		resp, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if fake.calls != 1 || u.attempts != 1 || fake.last.GetBody != nil || !service.HTTPUpstreamRedirectsDisabled(fake.last.Context()) {
			t.Fatal("actual transport was not guarded")
		}
		_, err = u.DoWithTLS(fidelityOfflineRequest(t), "", 15522, 1, &tlsfingerprint.Profile{})
		if err == nil || fake.calls != 1 || u.blocked != 1 || strings.Count(out.String(), "before_send") != 1 {
			t.Fatal("same slot retry escaped")
		}
		if strings.Contains(out.String(), "offline-fake") || strings.Contains(out.String(), "upstream.invalid") {
			t.Fatal("broker output leaked source")
		}
	})
	t.Run("deny_has_zero_calls", func(t *testing.T) {
		u, fake, _ := fidelityOfflineBudget(t, fidelityGrant{Type: "send_denied", Slot: 1})
		_, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		if err == nil || fake.calls != 0 || u.attempts != 0 {
			t.Fatal("denied call was sent")
		}
	})
	t.Run("different_source_has_zero_calls", func(t *testing.T) {
		u, fake, out := fidelityOfflineBudget(t, fidelityGrant{})
		u.h.boot.Source.Account.Credentials["api_key"] = "changed"
		_, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		if err == nil || fake.calls != 0 || out.Len() != 0 {
			t.Fatal("changed source passed verification")
		}
	})
	t.Run("bad_grant_and_budget_fail_closed", func(t *testing.T) {
		for _, grant := range []fidelityGrant{{Type: "send_granted", Slot: 1, Fingerprint: strings.Repeat("b", 64), Attempt: 1}, {Type: "send_granted", Slot: 1, Fingerprint: strings.Repeat("a", 64), Attempt: 2}, {Type: "send_granted", Slot: 1, Fingerprint: strings.Repeat("a", 64), Attempt: 1, ElapsedMS: fidelityMaxDuration.Milliseconds()}} {
			u, fake, _ := fidelityOfflineBudget(t, grant)
			_, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
			if err == nil || fake.calls != 0 {
				t.Fatal("invalid grant sent request")
			}
		}
		u, fake, _ := fidelityOfflineBudget(t, fidelityGrant{})
		u.attempts = 24
		_, err := u.Do(fidelityOfflineRequest(t), "", 15522, 1)
		if err == nil || fake.calls != 0 {
			t.Fatal("global budget exceeded")
		}
	})
	t.Run("cancelled_context_does_not_consume", func(t *testing.T) {
		u, fake, out := fidelityOfflineBudget(t, fidelityGrant{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := u.Do(fidelityOfflineRequest(t).WithContext(ctx), "", 15522, 1)
		if err == nil || fake.calls != 0 || out.Len() != 0 {
			t.Fatal("cancelled request consumed grant")
		}
	})
	t.Run("detached_forward_context_regains_slot_deadline", func(t *testing.T) {
		u, fake, _ := fidelityOfflineBudget(t, fidelityGrant{Type: "send_granted", Slot: 1, Fingerprint: strings.Repeat("a", 64), Attempt: 1})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		u.activeContext = ctx
		resp, err := u.Do(fidelityOfflineRequest(t).WithContext(context.WithoutCancel(ctx)), "", 15522, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := fake.last.Context().Deadline(); !ok {
			t.Fatal("detached Forward lost experiment deadline")
		}
		_ = resp.Body.Close()
		if fake.last.Context().Err() == nil {
			t.Fatal("upstream context not released on close")
		}
	})
	t.Run("bootstrap_exact_account_group_and_ledger", func(t *testing.T) {
		b := fidelityOfflineBootstrap()
		if err := fidelityValidateBootstrap(b); err != nil {
			t.Fatal(err)
		}
		b.Source.Account.Name = "another"
		if fidelityValidateBootstrap(b) == nil {
			t.Fatal("different account accepted")
		}
		b = fidelityOfflineBootstrap()
		b.Ledger = fidelityLedger{Attempts: 2, ConsumedSlots: []int{1, 1}}
		if fidelityValidateBootstrap(b) == nil {
			t.Fatal("duplicate ledger slot accepted")
		}
	})
	t.Run("tool_replay_preserves_raw_order_phase_unknowns", func(t *testing.T) {
		body := fidelityRequestBody("gpt-5.6-sol", "xhigh", fidelityOrdersPrompt, true)
		items := []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque","summary":[],"custom":true}`), json.RawMessage(`{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"loading"}]}`), json.RawMessage(`{"type":"function_call","call_id":"real_call","name":"load_orders","arguments":"{}"}`)}
		next, ok := fidelityToolContinuation(body, fidelityResponse{Status: "completed", Output: items})
		if !ok {
			t.Fatal("complete tool history rejected")
		}
		var v struct {
			Input      []json.RawMessage `json:"input"`
			ToolChoice string            `json:"tool_choice"`
		}
		if json.Unmarshal(next, &v) != nil || len(v.Input) != 5 || v.ToolChoice != "none" {
			t.Fatal("incorrect replay shape")
		}
		for i, item := range items {
			if !fidelityJSONEqual(item, v.Input[i+1]) {
				t.Fatal("raw output item lost")
			}
		}
		if bytes.Count(next, []byte("Select at most")) != 1 {
			t.Fatal("initial constraints were duplicated")
		}
		if _, ok := fidelityToolContinuation(body, fidelityResponse{Status: "incomplete", Output: items}); ok {
			t.Fatal("incomplete response replayed")
		}
	})
	t.Run("real_forward_is_reached_offline", func(t *testing.T) {
		b := fidelityOfflineBootstrap()
		fake := &fidelityOfflineUpstream{}
		cfg := &config.Config{}
		gateway, err := service.ReasoningFidelityGatewayForTest(cfg, fake, b.Source.BusinessPrompt, nil, b.Source.Settings)
		if err != nil {
			t.Fatal(err)
		}
		h := &fidelityHarness{boot: b, gateway: gateway}
		body := fidelityRequestBody("gpt-5.6-sol", "xhigh", fidelitySinglePrompt, false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c, r := h.newContext(ctx, body, "xhigh")
		_, err = gateway.Forward(c.Request.Context(), c, &h.boot.Source.Account, body)
		if err != nil || fake.calls != 1 || r.Code != 200 {
			t.Fatal("headless Forward did not complete")
		}
	})
	t.Run("group_policy_applied_once_before_both_arms", func(t *testing.T) {
		b := fidelityOfflineBootstrap()
		b.Source.Group.MaxReasoningEffort = "high"
		fake := &fidelityOfflineUpstream{}
		gateway, err := service.ReasoningFidelityGatewayForTest(&config.Config{}, fake, b.Source.BusinessPrompt, nil, b.Source.Settings)
		if err != nil {
			t.Fatal(err)
		}
		h := &fidelityHarness{boot: b, gateway: gateway}
		body, err := fidelityPrepareIngressBody(fidelityRequestBody("gpt-5.6-sol", "xhigh", fidelitySinglePrompt, false), b.Source)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := h.newContext(context.Background(), body, "xhigh")
		_, err = gateway.Forward(c.Request.Context(), c, &h.boot.Source.Account, body)
		if err != nil {
			t.Fatal("gateway policy test failed")
		}
		gatewayBody := append([]byte(nil), fake.body...)
		directContext, _ := h.newContext(context.Background(), body, "xhigh")
		req, err := service.ReasoningFidelityDirectRequestForTest(directContext.Request.Context(), gateway, directContext, &h.boot.Source.Account, body)
		if err != nil {
			t.Fatal("direct policy test failed")
		}
		directBody, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		var a, before map[string]json.RawMessage
		_ = json.Unmarshal(gatewayBody, &a)
		_ = json.Unmarshal(directBody, &before)
		if !fidelityJSONEqual(a["reasoning"], before["reasoning"]) || !bytes.Contains(before["reasoning"], []byte(`"high"`)) {
			t.Fatal("arms received different effective effort")
		}
	})
}
