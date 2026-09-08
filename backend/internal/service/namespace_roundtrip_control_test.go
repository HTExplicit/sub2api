//go:build reasoning_fidelity && namespace_roundtrip

package service_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type namespaceOfflineUpstream struct {
	calls                int
	last                 *http.Request
	bodies               [][]byte
	requestedEfforts     []string
	firstNamespaceAbsent bool
	firstIncomplete      bool
	firstFailureBody     string
	firstFailureStatus   int
	firstFailureType     string
}

func (f *namespaceOfflineUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	f.calls++
	f.last = request
	body, _ := io.ReadAll(request.Body)
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	requestedEffort := ""
	if effort := service.RequestedReasoningEffortFromContext(request.Context()); effort != nil {
		requestedEffort = *effort
	}
	f.requestedEfforts = append(f.requestedEfforts, requestedEffort)
	var sent struct {
		Model      string          `json:"model"`
		ToolChoice json.RawMessage `json:"tool_choice"`
	}
	_ = json.Unmarshal(body, &sent)
	if f.calls == 1 && f.firstFailureBody != "" {
		return &http.Response{StatusCode: f.firstFailureStatus, Header: http.Header{"Content-Type": {f.firstFailureType}}, Body: io.NopCloser(strings.NewReader(f.firstFailureBody))}, nil
	}
	var choice struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	_ = json.Unmarshal(sent.ToolChoice, &choice)
	identity := strconv.Itoa(f.calls)
	output := []any{map[string]any{"type": "reasoning", "id": "rs_offline_" + identity, "summary": []any{}, "encrypted_content": "offline-opaque", "unknown_property": []any{1, true}}}
	if choice.Name != "" {
		call := map[string]any{"type": "function_call", "id": "fc_offline_" + identity, "call_id": "call_offline_" + identity, "name": choice.Name, "arguments": "{}", "unknown_property": map[string]any{"keep": true}}
		if !f.firstNamespaceAbsent || f.calls != 1 {
			call["namespace"] = nil
			if choice.Namespace != "" {
				call["namespace"] = choice.Namespace
			}
		}
		output = append(output, call)
	} else {
		output = append(output, map[string]any{"type": "message", "role": "assistant", "phase": "final_answer", "content": []any{map[string]any{"type": "output_text", "text": namespaceFinalAnswer}}})
	}
	status := "completed"
	if f.firstIncomplete && f.calls == 1 {
		status = "incomplete"
	}
	response, _ := json.Marshal(map[string]any{"id": "resp_offline", "status": status, "model": sent.Model, "output": output, "usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}})
	frame := "data: {\"type\":\"response." + status + "\",\"response\":" + string(response) + "}\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frame))}, nil
}

func (f *namespaceOfflineUpstream) DoWithTLS(request *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(request, proxy, id, concurrency)
}

func namespaceOfflineBootstrap() namespaceBootstrap {
	prompt := "Frozen offline server prompt. Follow the isolated test instructions."
	modelMapping := make(map[string]any)
	channelModels := make(map[string]string)
	for _, profile := range namespaceProfiles() {
		modelMapping[profile.Model] = profile.UpstreamModel
		channelModels[profile.Model] = profile.Model
	}
	return namespaceBootstrap{SchemaVersion: 1, ConfigMode: "production_env", Mode: "namespace_roundtrip_r3", RunID: namespaceRunID,
		SourceSHA: strings.Repeat("a", 40), ManifestSHA: strings.Repeat("b", 64), PriorAttempts: 1, ParentLedgerSHA: namespaceParentLedger, AncestorLedgerSHA: namespaceAncestorLedger, Source: fidelitySource{
			Account: service.Account{ID: 16050, Name: "白嫖-dmxapi", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "offline-private-key", "base_url": "https://upstream.invalid/v1", "model_mapping": modelMapping},
				Extra:       map[string]any{"openai_responses_supported": true, "openai_responses_mode": "responses"}, GroupIDs: []int64{4}},
			Group: service.Group{ID: 4, Platform: service.PlatformOpenAI, Status: service.StatusActive}, FastPolicy: service.DefaultOpenAIFastPolicySettings(), Settings: map[string]string{},
			ChannelModels: channelModels, Fingerprint: strings.Repeat("c", 64), UserID: 920000016050,
			BusinessPrompt: service.BusinessSystemPromptSnapshot{Enabled: true, TemplateID: 1, VersionID: 1, TemplateVersion: 1, Revision: 1, Body: prompt, SHA256: fidelityHash([]byte(prompt)), ByteLength: len(prompt)},
		}}
}

func namespaceOfflineHarness(t *testing.T, grants []namespaceGrant, fake *namespaceOfflineUpstream, changes ...func(*namespaceBootstrap)) (*namespaceHarness, *bytes.Buffer) {
	t.Helper()
	var input, output bytes.Buffer
	for _, grant := range grants {
		if json.NewEncoder(&input).Encode(grant) != nil {
			t.Fatal("offline grant encoding failed")
		}
	}
	h := &namespaceHarness{boot: namespaceOfflineBootstrap(), input: bufio.NewReader(&input), output: json.NewEncoder(&output), ctx: context.Background(), started: time.Now(), cacheKeys: make(map[string]string), promptSHAs: make(map[string]string)}
	for _, change := range changes {
		change(&h.boot)
	}
	h.sourceSnapshotSHA = fidelityHashJSON(&h.boot.Source)
	u := &namespaceBudgetUpstream{h: h, inner: fake, used: make(map[int]bool), endpoint: "https://upstream.invalid/v1/responses", authorizationSHA: fidelityHash([]byte("Bearer offline-private-key"))}
	h.upstream = u
	var err error
	h.gateway, err = service.ReasoningFidelityGatewayForTest(&config.Config{}, u, h.boot.Source.BusinessPrompt, h.boot.Source.RegistryPublication, h.boot.Source.Settings)
	if err != nil {
		t.Fatal("offline gateway assembly failed")
	}
	return h, &output
}

func namespaceOfflineGrants() []namespaceGrant {
	turns := []struct {
		scenario string
		turn     int
	}{{"astra_flat", 1}, {"astra_flat", 2}, {"astra_flat", 3}, {"luna_flat", 1}, {"luna_flat", 2}}
	var grants []namespaceGrant
	for i, turn := range turns {
		grants = append(grants, namespaceGrant{Type: "send_granted", Scenario: turn.scenario, Turn: turn.turn, Slot: i + 1, Attempt: i + 1, Fingerprint: strings.Repeat("c", 64)})
	}
	return grants
}

func TestNamespaceRoundtripControl(t *testing.T) {
	t.Run("bootstrap_is_new_fixed_run_and_never_resume", func(t *testing.T) {
		boot := namespaceOfflineBootstrap()
		if namespaceValidateBootstrap(boot, boot.SourceSHA) != nil || boot.RunID != "responses-namespace-20260908-r3" ||
			!namespaceLiveSelectionValid("^TestNamespaceRoundtripLive$", "", "3") {
			t.Fatal("valid offline bootstrap rejected")
		}
		for _, selection := range [][3]string{
			{"^TestNamespaceRoundtripLive$", "", "2"},
			{"^TestNamespaceRoundtripLive$", "", ""},
			{"^TestNamespaceRoundtripLive$", "1", "3"},
			{"TestNamespaceRoundtrip", "", "3"},
		} {
			if namespaceLiveSelectionValid(selection[0], selection[1], selection[2]) {
				t.Fatal("different live revision or broad selection accepted")
			}
		}
		for _, change := range []func(*namespaceBootstrap){
			func(b *namespaceBootstrap) { b.Mode = "reasoning_fidelity" },
			func(b *namespaceBootstrap) { b.Mode = "namespace_roundtrip" },
			func(b *namespaceBootstrap) { b.Mode = "namespace_roundtrip_r2" },
			func(b *namespaceBootstrap) { b.RunID = "responses-namespace-20260908" },
			func(b *namespaceBootstrap) { b.RunID = "responses-namespace-20260908-r2" },
			func(b *namespaceBootstrap) { b.PriorAttempts = 0 },
			func(b *namespaceBootstrap) { b.PriorAttempts = 2 },
			func(b *namespaceBootstrap) { b.ParentLedgerSHA = "" },
			func(b *namespaceBootstrap) { b.ParentLedgerSHA = strings.Repeat("A", 64) },
			func(b *namespaceBootstrap) { b.ParentLedgerSHA = namespaceAncestorLedger },
			func(b *namespaceBootstrap) { b.AncestorLedgerSHA = "" },
			func(b *namespaceBootstrap) { b.AncestorLedgerSHA = namespaceParentLedger },
			func(b *namespaceBootstrap) { b.Source.Account.ID = 15522 },
			func(b *namespaceBootstrap) { b.Source.BusinessPrompt.Enabled = false },
			func(b *namespaceBootstrap) { b.Source.BusinessPrompt.ExposeServerPrompt = true },
			func(b *namespaceBootstrap) { b.Source.ChannelModels["gpt-6-astra"] = "other" },
			func(b *namespaceBootstrap) { b.Ledger.Attempts = 1 },
			func(b *namespaceBootstrap) { b.ManifestSHA = "" },
		} {
			candidate := namespaceOfflineBootstrap()
			change(&candidate)
			if namespaceValidateBootstrap(candidate, candidate.SourceSHA) == nil {
				t.Fatal("invalid bootstrap passed")
			}
		}
		if namespaceValidateBootstrap(boot, strings.Repeat("d", 40)) == nil {
			t.Fatal("different candidate source accepted")
		}
	})
	t.Run("raw_null_and_string_namespace_replay", func(t *testing.T) {
		for _, namespace := range []string{"null", `"namespace_probe"`} {
			body := namespaceInitialBody("gpt-6-astra", "ultra", "session", true)
			raw := `{"status":"completed","output":[{"type":"reasoning","id":"rs1","encrypted_content":"opaque","summary":[],"unknown":[true,1]},{"type":"function_call","id":"fc1","call_id":"call1","name":"namespace_probe_first","namespace":` + namespace + `,"arguments":"{}","unknown":{"preserve":true}}]}`
			parsed := fidelityParseResponse([]byte(raw), "application/json", 200)
			next, err := namespaceContinuation(body, parsed, namespaceFirstFunction, namespaceSecondFunction)
			if err != nil {
				t.Fatal("valid output history rejected")
			}
			var fields map[string]json.RawMessage
			var input []json.RawMessage
			_ = json.Unmarshal(next, &fields)
			_ = json.Unmarshal(fields["input"], &input)
			if len(input) != 5 || namespaceHasDeclaration(fields["tools"]) || namespaceCountFields(input) != 1 {
				t.Fatal("wrong replay shape")
			}
			for i, item := range parsed.Output {
				if !namespaceJSONEqual(item, input[i+1]) {
					t.Fatal("raw output field lost")
				}
			}
			parsed.Status = "incomplete"
			if _, err := namespaceContinuation(body, parsed, namespaceFirstFunction, ""); err == nil {
				t.Fatal("incomplete output was replayed")
			}
		}
	})
	t.Run("unknown_large_numbers_are_not_rounded_when_comparing", func(t *testing.T) {
		left := []byte(`{"unknown":9007199254740992,"namespace":null}`)
		right := []byte(`{"unknown":9007199254740993,"namespace":null}`)
		if namespaceJSONEqual(left, right) || !namespaceJSONEqual(left, []byte(`{ "namespace":null, "unknown":9007199254740992 }`)) || namespaceJSONEqual(left, append(append([]byte(nil), left...), []byte(` {}`)...)) {
			t.Fatal("lossless replay comparison did not distinguish complete JSON values")
		}
	})
	t.Run("remaining_five_posts_include_prior_attempt_in_total", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{}
		grants := namespaceOfflineGrants()
		h, output := namespaceOfflineHarness(t, grants, fake)
		h.runSequences()
		if h.stopped != "" || !h.astraCompleted || !h.lunaCompleted || !h.coverage || h.upstream.attempts != len(grants) || fake.calls != len(grants) {
			t.Fatalf("offline sequence failed: stopped=%s calls=%d expected=%d", h.stopped, fake.calls, len(grants))
		}
		if strings.Contains(output.String(), "offline-private-key") || strings.Contains(output.String(), "offline-opaque") || strings.Contains(output.String(), "Frozen offline") || strings.Contains(output.String(), "upstream.invalid") {
			t.Fatal("safe protocol leaked source material")
		}
		// Match the Python broker's per-turn contract: slot is cumulative,
		// attempt_result.attempts is not. A cumulative 2 on turn two would
		// halt the real batch even though the Go-only chain completed.
		decoder := json.NewDecoder(strings.NewReader(output.String()))
		sends, results := 0, 0
		for {
			var message struct {
				Type          string          `json:"type"`
				Scenario      string          `json:"scenario"`
				Turn          int             `json:"turn"`
				Slot          int             `json:"slot"`
				Attempts      int             `json:"attempts"`
				Completed     bool            `json:"completed"`
				Model         string          `json:"model"`
				Effort        string          `json:"effort"`
				WireEffort    string          `json:"wire_effort"`
				WireModel     string          `json:"wire_model"`
				ModelMatches  bool            `json:"model_matches"`
				EffortMatches bool            `json:"effort_matches"`
				ErrorDetail   json.RawMessage `json:"error_detail"`
			}
			if err := decoder.Decode(&message); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatal("invalid broker protocol output")
			}
			switch message.Type {
			case "before_send":
				sends++
				if message.Slot != sends || results != sends-1 {
					t.Fatal("broker sends are not cumulative and serial")
				}
			case "attempt_result":
				if results >= len(grants) || message.Attempts != 1 || !message.Completed || message.Slot != results+1 || sends != results+1 ||
					message.Scenario != grants[results].Scenario || message.Turn != grants[results].Turn || len(message.ErrorDetail) != 0 {
					t.Fatal("attempt result violates broker one-send turn contract")
				}
				profile, ok := namespaceProfileFor(message.Model, message.Effort)
				var sent struct {
					Model string `json:"model"`
				}
				if json.Unmarshal(fake.bodies[results], &sent) != nil || !ok || message.WireModel != profile.UpstreamModel ||
					sent.Model != profile.UpstreamModel || !message.ModelMatches || message.WireEffort != profile.WireEffort || !message.EffortMatches ||
					fake.requestedEfforts[results] != profile.WireEffort || !profile.matchesWireEffort(fake.bodies[results]) {
					t.Fatal("scenario label was substituted for actual wire or requested policy effort")
				}
				results++
			default:
				t.Fatal("unexpected turn protocol message")
			}
		}
		if sends != len(grants) || results != len(grants) {
			t.Fatal("broker protocol result count mismatch")
		}
		if summary := h.summary(); summary["status"] != "completed" || summary["attempts"] != 5 || summary["total_attempts"] != 6 {
			t.Fatal("summary lost the prior consumed request or exceeded the aggregate allowance")
		}
		if fake.last.GetBody != nil || !service.HTTPUpstreamRedirectsDisabled(fake.last.Context()) {
			t.Fatal("transport replay protections missing")
		}
	})
	t.Run("fixed_profiles_enforce_wire_effort_and_frozen_policy", func(t *testing.T) {
		for _, profile := range []struct{ model, label, upstream string }{{"gpt-6-astra", "ultra", "gpt-6-astra-ssvip"}, {"gpt-5.6-luna", "max", "gpt-5.6-luna"}} {
			fixed, ok := namespaceProfileFor(profile.model, profile.label)
			body := namespaceInitialBody(profile.model, profile.label, "profile-check", false)
			if !ok || fixed.UpstreamModel != profile.upstream || fixed.WireEffort != "max" || !fixed.matchesWireEffort(body) {
				t.Fatal("approved scenario did not derive max API effort")
			}
		}
		for _, profile := range []struct{ model, label string }{{"gpt-6-astra", "max"}, {"gpt-5.6-luna", "ultra"}, {"other", "max"}} {
			if _, ok := namespaceProfileFor(profile.model, profile.label); ok || namespaceInitialBody(profile.model, profile.label, "invalid-profile", false) != nil {
				t.Fatal("unapproved scenario bypassed the fixed profile entry")
			}
		}
		fake := &namespaceOfflineUpstream{}
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake)
		body := namespaceInitialBody("gpt-6-astra", "ultra", "wire-check", false)
		_, result := h.runTurn("astra_flat", 1, "gpt-6-astra", "ultra", body, namespaceFirstFunction)
		if !result.Completed || result.Effort != "ultra" || result.WireEffort != "max" || !result.EffortMatches || fake.requestedEfforts[0] != "max" || !h.upstream.validWireBody(fake.bodies[0]) {
			t.Fatal("approved profile was not applied before policy context and actual Forward")
		}
		for _, reasoning := range []string{`{"effort":"ultra"}`, `{"effort":"high"}`, `{"effort":"xhigh"}`, `{"effort":"max","mode":"pro"}`} {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(fake.bodies[0], &fields)
			fields["reasoning"] = json.RawMessage(reasoning)
			changed, _ := json.Marshal(fields)
			if h.upstream.validWireBody(changed) {
				t.Fatal("wire gate admitted a wrong API effort or an injected reasoning mode")
			}
		}
		policyFake := &namespaceOfflineUpstream{}
		policyHarness, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(), policyFake, func(boot *namespaceBootstrap) {
			boot.Source.Group.MaxReasoningEffort = "high"
			boot.Source.Group.MaxReasoningEffortOverLimit = service.ReasoningEffortOverLimitDowngrade
		})
		frozen := fidelityHashJSON(&policyHarness.boot.Source)
		policyHarness.runSequences()
		if policyHarness.stopped != "source_group_policy_rejected" || policyFake.calls != 0 || policyHarness.upstream.attempts != 0 || fidelityHashJSON(&policyHarness.boot.Source) != frozen {
			t.Fatal("frozen policy downgrade was hidden, bypassed, or sent upstream")
		}
	})
	t.Run("fixed_mapping_is_explicit_for_bootstrap_wire_and_safe_report", func(t *testing.T) {
		for _, profile := range namespaceProfiles() {
			boot := namespaceOfflineBootstrap()
			mapping, ok := boot.Source.Account.Credentials["model_mapping"].(map[string]any)
			if !ok {
				t.Fatal("invalid offline mapping fixture")
			}
			mapping[profile.Model] = "gpt-5.6-luna-ssvip"
			if namespaceValidateBootstrap(boot, boot.SourceSHA) == nil {
				t.Fatal("mismatched fixed account mapping admitted")
			}
			fake := &namespaceOfflineUpstream{}
			scenario := "astra_flat"
			if profile.Model == "gpt-5.6-luna" {
				scenario = "luna_flat"
			}
			grants := namespaceOfflineGrants()[:1]
			grants[0].Scenario = scenario
			h, _ := namespaceOfflineHarness(t, grants, fake)
			body := namespaceInitialBody(profile.Model, profile.EffortLabel, "mapping-check", false)
			_, result := h.runTurn(scenario, 1, profile.Model, profile.EffortLabel, body, namespaceFirstFunction)
			if !result.Completed || !result.ModelMatches || result.WireModel != profile.UpstreamModel {
				t.Fatal("actual Forward did not preserve the fixed upstream target")
			}
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(fake.bodies[0], &fields)
			fields["model"] = json.RawMessage(`"unapproved-target-private"`)
			changed, _ := json.Marshal(fields)
			if h.upstream.validWireBody(changed) {
				t.Fatal("wire gate admitted an unapproved target")
			}
			observation := namespaceResult{Model: profile.Model, Effort: profile.EffortLabel, WireEffort: profile.WireEffort}
			h.observeRequest(&observation, body, changed)
			if observation.ModelMatches || observation.WireModel != "" {
				t.Fatal("safe report exposed or accepted an unapproved target")
			}
		}
	})
	t.Run("hybrid_uses_frozen_published_prompt_not_template_substring", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{}
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake, func(boot *namespaceBootstrap) {
			boot.Source.BusinessPrompt.CompositionMode = service.BusinessSystemPromptCompositionCodexSkillHybrid
			boot.Source.BusinessPrompt.BundleID = service.BusinessSystemPromptRemoteSkillBundleID
			raw, effective := "Raw paired prompt.", "Published effective server instructions."
			boot.Source.RegistryPublication = &service.ReasoningFidelityPublicationSnapshot{Revision: 3,
				Version: service.RemoteSkillBundleVersion{ID: 2, PromptVersionID: 3, UpstreamSourceID: service.RemoteSkillUpstreamSourceID,
					UpstreamRoot: service.RemoteSkillUpstreamRoot, PublicRoot: service.RemoteSkillPublicRoot, RawTreeSHA256: strings.Repeat("d", 64), EffectiveTreeSHA256: strings.Repeat("e", 64)},
				Prompt: service.RemoteSkillPromptVersion{ID: 3, RawSHA256: fidelityHash([]byte(raw)), EffectiveSHA256: fidelityHash([]byte(effective))}, RawBody: raw, EffectiveBody: effective}
		})
		body := namespaceInitialBody("gpt-6-astra", "ultra", "session", false)
		_, result := h.runTurn("astra_flat", 1, "gpt-6-astra", "ultra", body, namespaceFirstFunction)
		if !result.Completed || !result.PromptApplied || fake.calls != 1 {
			t.Fatalf("frozen hybrid prompt did not satisfy wire contract: %s", result.ErrorClass)
		}
		if bytes.Contains(fake.bodies[0], []byte(h.boot.Source.BusinessPrompt.Body)) || !bytes.Contains(fake.bodies[0], []byte(h.boot.Source.RegistryPublication.EffectiveBody)) {
			t.Fatal("hybrid did not use the actual effective publication")
		}
	})
	t.Run("incomplete_stops_without_fallback_or_luna", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{firstIncomplete: true}
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake)
		h.runSequences()
		if fake.calls != 1 || h.upstream.attempts != 1 || h.stopped == "" || h.astraCompleted || h.lunaCompleted {
			t.Fatal("incomplete response spent an additional request")
		}
	})
	t.Run("denied_wrong_grant_and_wire_mutation_never_send", func(t *testing.T) {
		for _, mutate := range []func(*namespaceHarness){
			func(h *namespaceHarness) {
				h.input = bufio.NewReader(strings.NewReader(`{"type":"send_denied"}` + "\n"))
			},
			func(h *namespaceHarness) {
				h.input = bufio.NewReader(strings.NewReader(`{"type":"send_granted","slot":1,"scenario":"luna_flat","turn":1,"attempt":1,"fingerprint":"` + strings.Repeat("c", 64) + `"}` + "\n"))
			},
			func(h *namespaceHarness) { h.boot.Source.Account.Credentials["api_key"] = "different" },
			func(h *namespaceHarness) { h.boot.Source.Group.MaxReasoningEffort = "high" },
		} {
			fake := &namespaceOfflineUpstream{}
			h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake)
			mutate(h)
			h.runSequences()
			if fake.calls != 0 || h.upstream.attempts != 0 || h.stopped == "" {
				t.Fatal("ungranted or changed source sent a request")
			}
		}
	})
	t.Run("used_slot_and_total_limit_cannot_send", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{}
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake)
		body := namespaceInitialBody("gpt-6-astra", "ultra", "session", false)
		_, result := h.runTurn("astra_flat", 1, "gpt-6-astra", "ultra", body, namespaceFirstFunction)
		if !result.Completed {
			t.Fatal("single authorized fake send failed")
		}
		if _, err := h.upstream.DoWithTLS(fake.last, "", 16050, 1, &tlsfingerprint.Profile{}); err == nil || fake.calls != 1 || h.upstream.totalBlocked != 1 {
			t.Fatal("used slot allowed TLS or compatibility retry")
		}
		h.upstream.activeSlot, h.upstream.attempts = 5, 5
		if _, err := h.upstream.Do(nil, "", 16050, 1); err == nil || fake.calls != 1 {
			t.Fatal("five remaining attempts limit escaped")
		}
		h.upstream.activeSlot, h.upstream.attempts = 6, 4
		if _, err := h.upstream.Do(nil, "", 16050, 1); err == nil || fake.calls != 1 {
			t.Fatal("sixth local slot escaped")
		}
	})
	t.Run("no_namespace_stops_without_any_replacement_chain", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{firstNamespaceAbsent: true}
		h, output := namespaceOfflineHarness(t, namespaceOfflineGrants(), fake)
		h.runSequences()
		if fake.calls != 1 || h.upstream.attempts != 1 || h.stopped != "namespace_sample_unavailable" || h.coverage || h.astraCompleted || h.lunaCompleted ||
			strings.Contains(output.String(), "astra_namespace") || h.summary()["total_attempts"] != 2 {
			t.Fatal("missing namespace triggered a replacement chain or lost cumulative consumption")
		}
	})
}
