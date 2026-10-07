//go:build unit

package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// An id in the form a relay mints. The official upstream takes a reasoning
// item that carries such an id and no ciphertext for a reference to an item it
// stores, and does not find it.
const reasoningRecoveryRelayItemID = "rs_0123456789abcdef0123456789abcdef"

var reasoningRecoveryRelayFixture = strings.Replace(reasoningRecoveryFixture, "rs_old", reasoningRecoveryRelayItemID, 1)

func reasoningRecoveryJSON(status int, body string) *http.Response {
	resp := newJSONResponse(status, body)
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

// The official answer, as recorded in production.
func reasoningRecoveryUnfoundItem(id string) *http.Response {
	return reasoningRecoveryJSON(http.StatusNotFound, `{"error":{"message":"Item with id '`+id+`' not found. Items are not persisted when `+"`store`"+` is set to false. Try again with `+"`store`"+` set to true, or remove this item from your input.","type":"invalid_request_error","param":"input","code":null}}`)
}

func reasoningRecoveryCipherRejected() *http.Response {
	return reasoningRecoveryJSON(http.StatusBadRequest, `{"error":{"message":"The encrypted content could not be verified.","type":"invalid_request_error","param":null,"code":"invalid_encrypted_content"}}`)
}

func reasoningRecoveryCompleted() *http.Response {
	return reasoningRecoveryJSON(http.StatusOK, `{"id":"resp_final","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":11,"output_tokens":7}}`)
}

// reasoningRecoveryItemIDHarness forwards requests of one key to one account
// over a scripted upstream and a rejection memory that outlives a request.
type reasoningRecoveryItemIDHarness struct {
	svc      *OpenAIGatewayService
	store    *reasoningRecoveryMemoryStore
	upstream *httpUpstreamRecorder
	account  *Account
}

func newReasoningRecoveryItemIDHarness(passthrough bool) *reasoningRecoveryItemIDHarness {
	upstream := &httpUpstreamRecorder{}
	h := &reasoningRecoveryItemIDHarness{
		svc: newOpenAIImageGenerationControlTestService(upstream), upstream: upstream,
		store: &reasoningRecoveryMemoryStore{GatewayCache: &stubGatewayCache{}}, account: newOpenAIImageGenerationControlTestAccount(),
	}
	h.svc.cache = h.store
	h.account.Extra = map[string]any{"openai_passthrough": passthrough, "responses_api_supported": true}
	return h
}

type reasoningRecoveryItemIDAttempt struct {
	c        *gin.Context
	recorder *httptest.ResponseRecorder
	bodies   [][]byte
	err      error
}

// forward sends body once; the upstream answers with responses in order.
func (h *reasoningRecoveryItemIDHarness) forward(t *testing.T, body string, responses ...*http.Response) reasoningRecoveryItemIDAttempt {
	t.Helper()
	c, recorder := newOpenAIImageGenerationControlTestContext(false, "test-client")
	getAPIKeyFromContext(c).UserID = 77
	sent := len(h.upstream.bodies)
	h.upstream.responses = responses
	_, err := h.svc.Forward(context.Background(), c, h.account, []byte(body))
	require.Empty(t, h.upstream.responses, "every scripted answer was asked for")
	return reasoningRecoveryItemIDAttempt{c: c, recorder: recorder, bodies: h.upstream.bodies[sent:], err: err}
}

// learn makes the memory hold the ciphertext of body's reasoning items: the
// upstream rejects it once and accepts the stripped request.
func (h *reasoningRecoveryItemIDHarness) learn(t *testing.T, body string) {
	t.Helper()
	attempt := h.forward(t, body, reasoningRecoveryCipherRejected(), reasoningRecoveryCompleted())
	require.NoError(t, attempt.err)
	require.Len(t, attempt.bodies, 2)
	require.Equal(t, 1, h.store.puts)
}

func (a reasoningRecoveryItemIDAttempt) recoveryActions() []string {
	events, _ := a.c.Get(OpsUpstreamErrorsKey)
	var actions []string
	recorded, _ := events.([]*OpsUpstreamErrorEvent)
	for _, event := range recorded {
		if event.Kind == "reasoning_recovery" {
			actions = append(actions, event.Message)
		}
	}
	return actions
}

func forEachReasoningRecoveryPath(t *testing.T, run func(t *testing.T, h *reasoningRecoveryItemIDHarness)) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{true: "passthrough", false: "native"}[passthrough], func(t *testing.T) {
			run(t, newReasoningRecoveryItemIDHarness(passthrough))
		})
	}
}

// The upstream cannot find an item the gateway left behind without ciphertext.
// The request is sent once more with the ids of the stripped items removed and
// nothing else changed.
func TestOpenAIReasoningRecoveryRepairsAStrippedItemTheUpstreamCannotFind(t *testing.T) {
	t.Run("pre_stripped_first_send", func(t *testing.T) {
		forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
			h.learn(t, reasoningRecoveryRelayFixture)

			attempt := h.forward(t, reasoningRecoveryRelayFixture, reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCompleted())
			require.NoError(t, attempt.err)
			require.Len(t, attempt.bodies, 2, "the pre-stripped send and the repair")
			require.False(t, gjson.GetBytes(attempt.bodies[0], "input.1.encrypted_content").Exists())
			require.Equal(t, reasoningRecoveryRelayItemID, gjson.GetBytes(attempt.bodies[0], "input.1.id").String())
			withoutID, err := sjson.DeleteBytes(attempt.bodies[0], "input.1.id")
			require.NoError(t, err)
			require.Equal(t, string(withoutID), string(attempt.bodies[1]), "only the id of the stripped item is gone")
			require.Equal(t, "visible summary", gjson.GetBytes(attempt.bodies[1], "input.1.summary.0.text").String(), "the item itself stays")
			require.Equal(t, "fc_one", gjson.GetBytes(attempt.bodies[1], "input.2.id").String(), "no other item loses its id")
			require.Contains(t, attempt.recorder.Body.String(), "resp_final")
			require.Equal(t, []string{"rejected_history_skipped", "retry_without_item_ids"}, attempt.recoveryActions())
			require.Equal(t, "retry_without_item_ids", attempt.recorder.Header().Get(openAIReasoningRecoveryHeader))
			require.Equal(t, 1, h.store.puts, "the repair writes nothing to the memory")
		})
	})
	t.Run("stripped_retry", func(t *testing.T) {
		forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
			attempt := h.forward(t, reasoningRecoveryRelayFixture,
				reasoningRecoveryCipherRejected(), reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCompleted())
			require.NoError(t, attempt.err)
			require.Len(t, attempt.bodies, 3, "the first send, the stripped retry and the repair")
			require.Equal(t, "opaque-old", gjson.GetBytes(attempt.bodies[0], "input.1.encrypted_content").String())
			stripped, err := sjson.DeleteBytes(attempt.bodies[0], "input.1.encrypted_content")
			require.NoError(t, err)
			require.Equal(t, string(stripped), string(attempt.bodies[1]))
			withoutID, err := sjson.DeleteBytes(stripped, "input.1.id")
			require.NoError(t, err)
			require.Equal(t, string(withoutID), string(attempt.bodies[2]))
			require.Contains(t, attempt.recorder.Body.String(), "resp_final")
			require.Equal(t, []string{"retry_without_encrypted_content", "retry_without_item_ids"}, attempt.recoveryActions())
			require.Equal(t, 1, h.store.puts, "the rejected ciphertext only")
		})
	})
	// A relay may pass the official answer on under a status of its own.
	t.Run("answered_with_another_status", func(t *testing.T) {
		forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
			h.learn(t, reasoningRecoveryRelayFixture)
			wrapped := reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID)
			wrapped.StatusCode = http.StatusBadRequest

			attempt := h.forward(t, reasoningRecoveryRelayFixture, wrapped, reasoningRecoveryCompleted())
			require.NoError(t, attempt.err)
			require.Len(t, attempt.bodies, 2)
			require.False(t, gjson.GetBytes(attempt.bodies[1], "input.1.id").Exists())
		})
	})
}

// Nothing about the repair is remembered: the next request of the conversation
// is sent as before, meets the same answer and is repaired again.
func TestOpenAIReasoningRecoveryRepairIsMadeAgainOnTheNextRequest(t *testing.T) {
	forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
		h.learn(t, reasoningRecoveryRelayFixture)
		first := h.forward(t, reasoningRecoveryRelayFixture, reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCompleted())
		require.NoError(t, first.err)

		next := h.forward(t, reasoningRecoveryRelayFixture, reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCompleted())
		require.NoError(t, next.err)
		require.Len(t, next.bodies, 2)
		require.Equal(t, string(first.bodies[0]), string(next.bodies[0]), "the id is still sent first")
		require.Equal(t, string(first.bodies[1]), string(next.bodies[1]))
	})
}

// The upstream names the first item it cannot find. Every stripped item loses
// its id in the one repair, so the next one is not reported in turn.
func TestOpenAIReasoningRecoveryRepairRemovesTheIDOfEveryStrippedItem(t *testing.T) {
	forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
		body := strings.Replace(reasoningRecoveryRelayFixture, `"output":"[]"}`,
			`"output":"[]"},{"type":"reasoning","id":"rs_fedcba9876543210fedcba9876543210","summary":[],"encrypted_content":"opaque-second"}`, 1)
		h.learn(t, body)

		attempt := h.forward(t, body, reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCompleted())
		require.NoError(t, attempt.err)
		require.Len(t, attempt.bodies, 2)
		for _, index := range []string{"1", "4"} {
			require.True(t, gjson.GetBytes(attempt.bodies[0], "input."+index+".id").Exists())
			require.False(t, gjson.GetBytes(attempt.bodies[0], "input."+index+".encrypted_content").Exists())
			require.Equal(t, "reasoning", gjson.GetBytes(attempt.bodies[1], "input."+index+".type").String())
			require.False(t, gjson.GetBytes(attempt.bodies[1], "input."+index+".id").Exists())
		}
	})
}

// A repair that happens before any stripped retry leaves that retry available:
// ciphertext the upstream then rejects is still stripped once, and its item
// loses the id with it.
func TestOpenAIReasoningRecoveryRepairLeavesTheStrippedRetryAvailable(t *testing.T) {
	forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
		h.learn(t, reasoningRecoveryRelayFixture)
		body := strings.Replace(reasoningRecoveryRelayFixture, `"output":"[]"}`,
			`"output":"[]"},{"type":"reasoning","id":"rs_fedcba9876543210fedcba9876543210","summary":[],"encrypted_content":"opaque-new"}`, 1)

		attempt := h.forward(t, body,
			reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCipherRejected(), reasoningRecoveryCompleted())
		require.NoError(t, attempt.err)
		require.Len(t, attempt.bodies, 3)
		require.Equal(t, "opaque-new", gjson.GetBytes(attempt.bodies[1], "input.4.encrypted_content").String())
		require.False(t, gjson.GetBytes(attempt.bodies[1], "input.1.id").Exists())
		for _, index := range []string{"1", "4"} {
			require.Equal(t, "reasoning", gjson.GetBytes(attempt.bodies[2], "input."+index+".type").String())
			require.False(t, gjson.GetBytes(attempt.bodies[2], "input."+index+".encrypted_content").Exists())
			require.False(t, gjson.GetBytes(attempt.bodies[2], "input."+index+".id").Exists(), "after the repair a strip takes the id too")
		}
		require.Equal(t, []string{"rejected_history_skipped", "retry_without_item_ids", "retry_without_encrypted_content"}, attempt.recoveryActions())
	})
}

// The repair is made once in an attempt.
func TestOpenAIReasoningRecoveryRepairsOnce(t *testing.T) {
	validation := func() *http.Response {
		return reasoningRecoveryJSON(http.StatusBadRequest, `{"error":{"message":"Missing required parameter: 'input[1].id'.","type":"invalid_request_error","param":"input[1].id","code":"missing_required_parameter"}}`)
	}
	for _, test := range []struct {
		name   string
		answer func() *http.Response
	}{
		{name: "not_found_again", answer: func() *http.Response { return reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID) }},
		{name: "rejected", answer: validation},
	} {
		t.Run(test.name, func(t *testing.T) {
			forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
				h.learn(t, reasoningRecoveryRelayFixture)

				attempt := h.forward(t, reasoningRecoveryRelayFixture, reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), test.answer())
				require.Error(t, attempt.err)
				require.Len(t, attempt.bodies, 2, "no second repair")
				require.Equal(t, 1, h.store.puts)
			})
		})
	}
	t.Run("after_the_stripped_retry", func(t *testing.T) {
		forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
			attempt := h.forward(t, reasoningRecoveryRelayFixture, reasoningRecoveryCipherRejected(),
				reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID))
			require.Len(t, attempt.bodies, 3)
			var stopped *OpenAIReasoningRecoveryTerminalError
			require.ErrorAs(t, attempt.err, &stopped, "the stripped retry is spent: the request ends on this account")
			require.Equal(t, http.StatusNotFound, stopped.Failure.StatusCode)
			recovery := lastReasoningRecoveryDiagnostic(t, attempt.c).ContinuationDiagnostic.Recovery
			require.Equal(t, 1, recovery.ItemIDsRemoved)
			require.True(t, recovery.RetryAttempted, "the repaired body was sent")
		})
	})
}

// Only the answer naming an item this attempt stripped is repaired.
func TestOpenAIReasoningRecoveryLeavesEveryOtherMissingItemAlone(t *testing.T) {
	const clientItemID = "rs_ffffffffffffffffffffffffffffffff"
	withClientItem := strings.Replace(reasoningRecoveryRelayFixture, `"output":"[]"}`,
		`"output":"[]"},{"type":"reasoning","id":"`+clientItemID+`","summary":[]}`, 1)
	withoutSummary := strings.Replace(reasoningRecoveryRelayFixture, `"summary":[{"type":"summary_text","text":"visible summary"}],`, ``, 1)
	for _, test := range []struct {
		name   string
		learn  string
		body   string
		answer func() *http.Response
	}{
		{
			name: "an_item_the_client_sent_without_ciphertext", learn: reasoningRecoveryRelayFixture, body: withClientItem,
			answer: func() *http.Response { return reasoningRecoveryUnfoundItem(clientItemID) },
		},
		{
			name: "an_id_that_is_not_in_the_request", learn: reasoningRecoveryRelayFixture, body: reasoningRecoveryRelayFixture,
			answer: func() *http.Response { return reasoningRecoveryUnfoundItem("rs_00000000000000000000000000000000") },
		},
		{
			name: "nothing_was_stripped", body: reasoningRecoveryRelayFixture,
			answer: func() *http.Response { return reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID) },
		},
		{
			// Without a summary the upstream refuses the item once its id is gone.
			name: "an_item_without_a_summary", learn: withoutSummary, body: withoutSummary,
			answer: func() *http.Response { return reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID) },
		},
		{
			name: "a_status_that_says_something_about_the_account", learn: reasoningRecoveryRelayFixture, body: reasoningRecoveryRelayFixture,
			answer: func() *http.Response {
				forbidden := reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID)
				forbidden.StatusCode = http.StatusForbidden
				return forbidden
			},
		},
		{
			name: "another_message", learn: reasoningRecoveryRelayFixture, body: reasoningRecoveryRelayFixture,
			answer: func() *http.Response {
				return reasoningRecoveryJSON(http.StatusNotFound, `{"error":{"message":"The model `+"`"+reasoningRecoveryRelayItemID+"`"+` does not exist.","type":"invalid_request_error","param":"input","code":null}}`)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
				if test.learn != "" {
					h.learn(t, test.learn)
				}
				attempt := h.forward(t, test.body, test.answer())
				require.Error(t, attempt.err)
				require.Len(t, attempt.bodies, 1, "the request is not sent again")
				require.NotContains(t, attempt.recoveryActions(), "retry_without_item_ids")
			})
		})
	}
}

// An item without a summary keeps its id also when a strip follows the repair.
func TestOpenAIReasoningRecoveryKeepsTheIDOfAnItemWithoutSummary(t *testing.T) {
	forEachReasoningRecoveryPath(t, func(t *testing.T, h *reasoningRecoveryItemIDHarness) {
		h.learn(t, reasoningRecoveryRelayFixture)
		const bareID = "rs_fedcba9876543210fedcba9876543210"
		body := strings.Replace(reasoningRecoveryRelayFixture, `"output":"[]"}`,
			`"output":"[]"},{"type":"reasoning","id":"`+bareID+`","encrypted_content":"opaque-new"}`, 1)

		attempt := h.forward(t, body,
			reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID), reasoningRecoveryCipherRejected(), reasoningRecoveryCompleted())
		require.NoError(t, attempt.err)
		require.Len(t, attempt.bodies, 3)
		require.False(t, gjson.GetBytes(attempt.bodies[2], "input.1.id").Exists())
		require.False(t, gjson.GetBytes(attempt.bodies[2], "input.4.encrypted_content").Exists())
		require.Equal(t, bareID, gjson.GetBytes(attempt.bodies[2], "input.4.id").String())
	})
}

// The repair acts on the body that was sent: an item it stripped must be there,
// at its place, with its id and without ciphertext.
func TestOpenAIReasoningRecoveryRepairChecksWhatItStrippedAgainstWhatWasSent(t *testing.T) {
	unfound := []byte(`{"error":{"message":"Item with id '` + reasoningRecoveryRelayItemID + `' not found."}}`)
	prepared := func(t *testing.T) *openAIReasoningRecoveryState {
		state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryRelayFixture)
		_, repaired := state.TryRepairUnfoundItemIDs(http.StatusNotFound, nil, unfound)
		require.False(t, repaired, "nothing has been stripped")
		_, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
		require.True(t, retry)
		return state
	}

	t.Run("the_stripped_body_was_not_sent", func(t *testing.T) {
		state := prepared(t)
		_, repaired := state.TryRepairUnfoundItemIDs(http.StatusNotFound, nil, unfound)
		require.False(t, repaired, "the body last sent still holds the ciphertext")
	})
	for name, sent := range map[string]func(wire []byte) []byte{
		"another_id_at_its_place": func(wire []byte) []byte {
			out, _ := sjson.SetBytes(wire, "input.1.id", "rs_ffffffffffffffffffffffffffffffff")
			return out
		},
		"another_item_at_its_place": func(wire []byte) []byte {
			out, _ := sjson.SetBytes(wire, "input.1.type", "message")
			return out
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := prepared(t)
			state.wire = sent(state.retryBody)
			_, repaired := state.TryRepairUnfoundItemIDs(http.StatusNotFound, nil, unfound)
			require.False(t, repaired)
		})
	}
	t.Run("sent_as_stripped", func(t *testing.T) {
		state := prepared(t)
		state.MarkAttemptDispatched()
		state.wire = bytes.Clone(state.retryBody)
		body, repaired := state.TryRepairUnfoundItemIDs(http.StatusNotFound, nil, unfound)
		require.True(t, repaired)
		require.False(t, gjson.GetBytes(body, "input.1.id").Exists())
		require.Equal(t, string(body), string(state.retryBody), "the send boundary accepts the repaired body")
		require.False(t, state.retryDispatched, "which is not sent yet")
		_, again := state.TryRepairUnfoundItemIDs(http.StatusNotFound, nil, unfound)
		require.False(t, again)
	})
}

// A Responses-shaped body on the Chat Completions endpoint is stripped by the
// same rules and is repaired the same way.
func TestOpenAIReasoningRecoveryChatPathRepairsAStrippedItemTheUpstreamCannotFind(t *testing.T) {
	store := &reasoningRecoveryMemoryStore{GatewayCache: &stubGatewayCache{}}
	account := newOpenAIRejectedFieldTestAccount()
	fixture := strings.Replace(reasoningRecoveryChatFixture, `{"type":"reasoning",`, `{"type":"reasoning","id":"`+reasoningRecoveryRelayItemID+`",`, 1)
	body, err := sjson.SetBytes([]byte(fixture), "stream", true)
	require.NoError(t, err)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		reasoningRecoveryCipherRejected(),
		reasoningRecoveryUnfoundItem(reasoningRecoveryRelayItemID),
		reasoningRecoverySSEResponse("data: "+reasoningRecoveryChatCompleted+"\n\n", http.StatusOK),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	svc.cache = store
	c, _ := reasoningRecoveryChatContext(t, body)

	_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3, "the first send, the stripped retry and the repair")
	require.Equal(t, reasoningRecoveryRelayItemID, gjson.GetBytes(upstream.bodies[1], "input.1.id").String())
	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[2], "input.1.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "input.1.id").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "input.1.encrypted_content").Exists())
}

// The edit is repeated on the clean body exactly, also where the wire body
// holds an item the clean body does not, and nothing else passes as an edit.
func TestOpenAIReasoningRecoveryProjectionRepeatsOnlyRecoveryEdits(t *testing.T) {
	const (
		system  = `{"type":"message","role":"developer","content":"system"}`
		message = `{"type":"message","id":"msg_1","role":"user","content":"hello"}`
	)
	body := func(items ...string) string { return `{"model":"m","input":[` + strings.Join(items, ",") + `]}` }
	cipher := func(id, text string) string {
		return `{"type":"reasoning","id":"` + id + `","summary":[],"encrypted_content":"` + text + `"}`
	}
	bare := func(id string) string { return `{"type":"reasoning","id":"` + id + `","summary":[]}` }
	const idless = `{"type":"reasoning","summary":[]}`

	for _, test := range []struct {
		name, clean, before, after, want string
	}{
		{
			name:  "ciphertext_only",
			clean: body(cipher("rs_a", "a"), message), before: body(system, cipher("rs_a", "a"), message),
			after: body(system, bare("rs_a"), message), want: body(bare("rs_a"), message),
		},
		{
			name:  "ciphertext_and_id",
			clean: body(cipher("rs_a", "a"), cipher("rs_b", "b")), before: body(system, cipher("rs_a", "a"), cipher("rs_b", "b")),
			after: body(system, idless, cipher("rs_b", "b")), want: body(idless, cipher("rs_b", "b")),
		},
		{
			name:  "id_of_an_item_left_behind",
			clean: body(bare("rs_a"), message, bare("rs_b")), before: body(system, bare("rs_a"), message, bare("rs_b")),
			after: body(system, bare("rs_a"), message, idless), want: body(bare("rs_a"), message, idless),
		},
		{
			name:  "second_of_two_items_with_one_id",
			clean: body(bare("rs_a"), bare("rs_a")), before: body(system, bare("rs_a"), bare("rs_a")),
			after: body(system, bare("rs_a"), idless), want: body(bare("rs_a"), idless),
		},
		{
			name:  "the_id_of_another_item",
			clean: body(bare("rs_a"), message), before: body(bare("rs_a"), message),
			after: body(bare("rs_a"), `{"type":"message","role":"user","content":"hello"}`),
		},
		{
			name:  "the_id_of_an_item_that_keeps_its_ciphertext",
			clean: body(cipher("rs_a", "a")), before: body(cipher("rs_a", "a")),
			after: body(`{"type":"reasoning","summary":[],"encrypted_content":"a"}`),
		},
		{
			name:  "any_other_field",
			clean: body(bare("rs_a")), before: body(bare("rs_a")),
			after: body(`{"type":"reasoning","id":"rs_a"}`),
		},
		{
			name:  "an_item_the_clean_body_does_not_hold",
			clean: body(message), before: body(bare("rs_a"), message),
			after: body(idless, message),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			projected, err := projectReasoningRecoveryEdits([]byte(test.clean), []byte(test.before), []byte(test.after))
			if test.want == "" {
				require.ErrorIs(t, err, errReasoningWireProjection)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, string(projected))
		})
	}
}
