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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type namespaceOfflineUpstream struct {
	calls                int
	last                 *http.Request
	bodies               [][]byte
	firstNamespaceAbsent bool
	firstIncomplete      bool
}

func (f *namespaceOfflineUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	f.calls++
	f.last = request
	body, _ := io.ReadAll(request.Body)
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	var sent struct {
		Model      string          `json:"model"`
		ToolChoice json.RawMessage `json:"tool_choice"`
	}
	_ = json.Unmarshal(body, &sent)
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
	return namespaceBootstrap{SchemaVersion: 1, ConfigMode: "production_env", Mode: "namespace_roundtrip", RunID: namespaceRunID,
		SourceSHA: strings.Repeat("a", 40), ManifestSHA: strings.Repeat("b", 64), Source: fidelitySource{
			Account: service.Account{ID: 16050, Name: "白嫖-dmxapi", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "offline-private-key", "base_url": "https://upstream.invalid/v1", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra-ssvip", "gpt-5.6-luna": "gpt-5.6-luna-ssvip"}},
				Extra:       map[string]any{"openai_responses_supported": true, "openai_responses_mode": "responses"}, GroupIDs: []int64{4}},
			Group: service.Group{ID: 4, Platform: service.PlatformOpenAI, Status: service.StatusActive}, FastPolicy: service.DefaultOpenAIFastPolicySettings(), Settings: map[string]string{},
			ChannelModels: map[string]string{"gpt-6-astra": "gpt-6-astra", "gpt-5.6-luna": "gpt-5.6-luna"}, Fingerprint: strings.Repeat("c", 64), UserID: 920000016050,
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
	h := &namespaceHarness{boot: namespaceOfflineBootstrap(), input: bufio.NewReader(&input), output: json.NewEncoder(&output), ctx: context.Background(), cacheKeys: make(map[string]string), promptSHAs: make(map[string]string)}
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

func namespaceOfflineGrants(alternate bool) []namespaceGrant {
	turns := []struct {
		scenario string
		turn     int
	}{{"astra_flat", 1}, {"astra_flat", 2}, {"astra_flat", 3}, {"luna_flat", 1}, {"luna_flat", 2}}
	if alternate {
		turns = []struct {
			scenario string
			turn     int
		}{{"astra_flat", 1}, {"astra_namespace", 1}, {"astra_namespace", 2}, {"astra_namespace", 3}, {"luna_flat", 1}, {"luna_flat", 2}}
	}
	var grants []namespaceGrant
	for i, turn := range turns {
		grants = append(grants, namespaceGrant{Type: "send_granted", Scenario: turn.scenario, Turn: turn.turn, Slot: i + 1, Attempt: i + 1, Fingerprint: strings.Repeat("c", 64)})
	}
	return grants
}

func TestNamespaceRoundtripControl(t *testing.T) {
	t.Run("bootstrap_is_new_fixed_run_and_never_resume", func(t *testing.T) {
		boot := namespaceOfflineBootstrap()
		if namespaceValidateBootstrap(boot, boot.SourceSHA) != nil {
			t.Fatal("valid offline bootstrap rejected")
		}
		for _, change := range []func(*namespaceBootstrap){
			func(b *namespaceBootstrap) { b.Mode = "reasoning_fidelity" },
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
	t.Run("normal_five_posts_and_authorized_six_post_branch", func(t *testing.T) {
		for _, alternate := range []bool{false, true} {
			fake := &namespaceOfflineUpstream{firstNamespaceAbsent: alternate}
			grants := namespaceOfflineGrants(alternate)
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
					Type      string `json:"type"`
					Scenario  string `json:"scenario"`
					Turn      int    `json:"turn"`
					Slot      int    `json:"slot"`
					Attempts  int    `json:"attempts"`
					Completed bool   `json:"completed"`
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
						message.Scenario != grants[results].Scenario || message.Turn != grants[results].Turn {
						t.Fatal("attempt result violates broker one-send turn contract")
					}
					results++
				default:
					t.Fatal("unexpected turn protocol message")
				}
			}
			if sends != len(grants) || results != len(grants) {
				t.Fatal("broker protocol result count mismatch")
			}
			if fake.last.GetBody != nil || !service.HTTPUpstreamRedirectsDisabled(fake.last.Context()) {
				t.Fatal("transport replay protections missing")
			}
		}
	})
	t.Run("hybrid_uses_frozen_published_prompt_not_template_substring", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{}
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(false), fake, func(boot *namespaceBootstrap) {
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
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(false), fake)
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
			h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(false), fake)
			mutate(h)
			h.runSequences()
			if fake.calls != 0 || h.upstream.attempts != 0 || h.stopped == "" {
				t.Fatal("ungranted or changed source sent a request")
			}
		}
	})
	t.Run("used_slot_and_total_limit_cannot_send", func(t *testing.T) {
		fake := &namespaceOfflineUpstream{}
		h, _ := namespaceOfflineHarness(t, namespaceOfflineGrants(false), fake)
		body := namespaceInitialBody("gpt-6-astra", "ultra", "session", false)
		_, result := h.runTurn("astra_flat", 1, "gpt-6-astra", "ultra", body, namespaceFirstFunction)
		if !result.Completed {
			t.Fatal("single authorized fake send failed")
		}
		if _, err := h.upstream.DoWithTLS(fake.last, "", 16050, 1, &tlsfingerprint.Profile{}); err == nil || fake.calls != 1 || h.upstream.totalBlocked != 1 {
			t.Fatal("used slot allowed TLS or compatibility retry")
		}
		h.upstream.activeSlot, h.upstream.attempts = 6, 6
		if _, err := h.upstream.Do(nil, "", 16050, 1); err == nil || fake.calls != 1 {
			t.Fatal("six-attempt limit escaped")
		}
	})
}
