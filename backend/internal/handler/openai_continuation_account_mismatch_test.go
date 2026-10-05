//go:build unit

package handler

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// continuationMismatchUpstream answers by account: an accepting account
// completes the request, every other account answers with its scripted reply
// or, by default, rejects the ciphertext in the request.
type continuationMismatchUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
	bodies     [][]byte
	accepting  map[int64]bool
	replies    map[int64]continuationMismatchReply
	rejection  continuationMismatchReply
}

type continuationMismatchReply struct {
	status  int
	payload string
	// stream, when set, is sent as an event stream in place of payload.
	stream string
}

func (u *continuationMismatchUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.bodies = append(u.bodies, body)
	u.mu.Unlock()
	if u.accepting[accountID] {
		return openAIOfficialHTTPSuccess(openAIOfficialHTTPAttempt{accountID: accountID, path: req.URL.Path, body: body}), nil
	}
	reply, scripted := u.replies[accountID]
	if !scripted {
		reply = u.rejection
	}
	if reply.stream != "" {
		return openAIOfficialHTTPResponse(reply.status, "text/event-stream", reply.stream), nil
	}
	return openAIOfficialHTTPResponse(reply.status, "application/json", reply.payload), nil
}

func (u *continuationMismatchUpstream) distinctAccounts() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []int64
	seen := map[int64]bool{}
	for _, id := range u.accountIDs {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

var (
	// No item is named: with another ciphertext carrier nothing is removed.
	continuationMismatchUnrecoverable = continuationMismatchReply{status: http.StatusBadRequest,
		payload: `{"error":{"message":"invalid encrypted content","type":"invalid_request_error","param":null,"code":"invalid_encrypted_content"}}`}
	// The rejection names the reasoning item, so its ciphertext is removed and
	// the request is sent once more.
	continuationMismatchNamedReasoning = continuationMismatchReply{status: http.StatusBadRequest,
		payload: `{"error":{"message":"The encrypted content for item rs_a could not be verified.","type":"invalid_request_error","param":null,"code":"invalid_encrypted_content"}}`}
	// The wording for an inter-agent message body, which carries no code.
	continuationMismatchUndecryptable = continuationMismatchReply{status: http.StatusBadRequest,
		payload: `{"error":{"type":"invalid_request_error","message":"Encrypted function output content could not be decrypted or decoded."}}`}
)

func continuationMismatchAccounts(count int, accountType string) []service.Account {
	accounts := make([]service.Account, 0, count)
	for id := int64(1); id <= int64(count); id++ {
		account := service.Account{
			ID: id, Name: "organisation", Platform: service.PlatformOpenAI, Type: accountType,
			Status: service.StatusActive, Schedulable: true, Priority: int(id), GroupIDs: []int64{openAIOfficialHTTPGroupID},
			Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test"},
			Extra:       map[string]any{"use_responses_api": true},
		}
		if accountType == service.AccountTypeOAuth {
			account.Credentials = map[string]any{"access_token": "fixture"}
		}
		accounts = append(accounts, account)
	}
	return accounts
}

// newContinuationMismatchHandler runs the real handler, selector and forwarder
// over in-memory accounts and a sticky-session cache the test can read.
func newContinuationMismatchHandler(t *testing.T, upstream service.HTTPUpstream, accounts []service.Account) (*OpenAIGatewayHandler, *openAIOfficialHTTPStickyCache) {
	t.Helper()
	repo := &openAIOfficialHTTPAccountRepo{grokCredentialHandlerRepo: grokCredentialHandlerRepo{accounts: accounts}}
	cache := &openAIOfficialHTTPStickyCache{grokCredentialHandlerGatewayCache: grokCredentialHandlerGatewayCache{
		sessions: make(map[grokCredentialHandlerGatewayCacheKey]int64),
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 10
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, cache, cfg, nil, nil,
		service.NewBillingService(cfg, nil), service.NewRateLimitService(repo, nil, cfg, nil, nil), billing, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	return h, cache
}

func serveContinuationMismatch(t *testing.T, h *OpenAIGatewayHandler, body string) *responseRecorderResult {
	t.Helper()
	c, recorder := newReplayableEndpointContext(t, openAIOfficialHTTPGroupID, http.MethodPost, "/v1/responses", []byte(body))
	c.Request.Header.Set("session_id", "one-conversation")
	h.Responses(c)
	return &responseRecorderResult{code: recorder.Code, body: recorder.Body.String()}
}

type responseRecorderResult struct {
	code int
	body string
}

func continuationMismatchBoundAccount(t *testing.T, cache *openAIOfficialHTTPStickyCache) int64 {
	t.Helper()
	// The cache also holds response-owner entries; the session binding is the
	// entry keyed by the session hash alone.
	var bound []int64
	for key, accountID := range cache.sessions {
		if strings.Count(key.sessionHash, ":") == 1 {
			bound = append(bound, accountID)
		}
	}
	require.Len(t, bound, 1, "one conversation has one binding")
	return bound[0]
}

// A Codex multi-agent history: reasoning ciphertext plus an inter-agent message
// whose body is ciphertext the gateway cannot remove.
const continuationMismatchBody = `{"model":"gpt-5.1","stream":false,"input":[{"type":"reasoning","id":"rs_a","encrypted_content":"cipher-reasoning","summary":[]},{"type":"agent_message","id":"amsg_b","author":"/root","recipient":"/root/worker","content":[{"type":"input_text","text":"Payload:"},{"type":"encrypted_content","encrypted_content":"cipher-agent"}]}]}`

func TestOpenAIGatewayHandler_ContinuationCiphertextMismatchMovesToAnAccountThatAccepts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name         string
		rejection    continuationMismatchReply
		callsOnFirst int
	}{
		// The stripped retry is rejected again, as when the message body is foreign too.
		{"stripped_retry_rejected", continuationMismatchNamedReasoning, 2},
		{"recovery_not_possible", continuationMismatchUnrecoverable, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &continuationMismatchUpstream{accepting: map[int64]bool{2: true}, rejection: test.rejection}
			handler, cache := newContinuationMismatchHandler(t, upstream, continuationMismatchAccounts(2, service.AccountTypeAPIKey))
			response := serveContinuationMismatch(t, handler, continuationMismatchBody)

			require.Equal(t, http.StatusOK, response.code, response.body)
			require.Contains(t, response.body, "healthy answer")
			require.NotContains(t, response.body, service.OpenAIContinuationStateUnavailableCode)
			require.Len(t, upstream.accountIDs, test.callsOnFirst+1)
			require.Equal(t, []int64{1, 2}, upstream.distinctAccounts())
			accepted := upstream.bodies[len(upstream.bodies)-1]
			for _, path := range []string{"input.0.encrypted_content", "input.1"} {
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], path).Raw, gjson.GetBytes(accepted, path).Raw, "the next account receives the request unchanged: "+path)
			}
			require.Equal(t, "cipher-reasoning", gjson.GetBytes(accepted, "input.0.encrypted_content").String())
			require.Equal(t, int64(2), continuationMismatchBoundAccount(t, cache), "the conversation continues on the account that accepted it")
		})
	}
}

func TestOpenAIGatewayHandler_ContinuationCiphertextMismatchEndsWhenNoAccountAccepts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &continuationMismatchUpstream{rejection: continuationMismatchUnrecoverable}
	handler, cache := newContinuationMismatchHandler(t, upstream, continuationMismatchAccounts(maxOpenAICiphertextAccountSwitches+3, service.AccountTypeAPIKey))
	response := serveContinuationMismatch(t, handler, continuationMismatchBody)

	require.Len(t, upstream.distinctAccounts(), maxOpenAICiphertextAccountSwitches+1, "the first account and a bounded number of others")
	require.Len(t, upstream.accountIDs, maxOpenAICiphertextAccountSwitches+1, "an account that cannot repair the request is asked once")
	require.Equal(t, http.StatusBadRequest, response.code)
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, gjson.Get(response.body, "error.code").String())
	require.NotContains(t, response.body, "invalid encrypted content", "the client receives the fixed sentence, not upstream text")
	require.Equal(t, upstream.accountIDs[0], continuationMismatchBoundAccount(t, cache), "the accounts that rejected the conversation are no home for it")
}

// An account that reads the ciphertext and rejects the request for another
// reason has answered: that answer ends the request.
func TestOpenAIGatewayHandler_ContinuationCiphertextMismatchKeepsALaterAccountsOwnAnswer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &continuationMismatchUpstream{
		rejection: continuationMismatchUnrecoverable,
		replies: map[int64]continuationMismatchReply{2: {status: http.StatusBadRequest,
			payload: `{"error":{"type":"invalid_request_error","code":"missing_required_parameter","param":"input[3].call_id","message":"do-not-leak-validation"}}`}},
	}
	handler, cache := newContinuationMismatchHandler(t, upstream, continuationMismatchAccounts(3, service.AccountTypeAPIKey))
	response := serveContinuationMismatch(t, handler, continuationMismatchBody)

	require.Equal(t, []int64{1, 2}, upstream.accountIDs, "a validation rejection is the same on every account")
	require.Equal(t, http.StatusBadRequest, response.code)
	require.Equal(t, service.OpenAIRequestRejectedCode, gjson.Get(response.body, "error.code").String())
	require.NotContains(t, response.body, "do-not-leak")
	require.Equal(t, int64(1), continuationMismatchBoundAccount(t, cache))
}

// A ciphertext rejection wrapped in a 5xx is still no fault of the account:
// the account is not asked again before the next one is tried.
func TestOpenAIGatewayHandler_ContinuationCiphertextMismatchSkipsTheSameAccountRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &continuationMismatchUpstream{
		accepting: map[int64]bool{2: true},
		rejection: continuationMismatchReply{status: http.StatusServiceUnavailable, payload: `{"error":{"message":"Encrypted content could not be verified"}}`},
	}
	handler, _ := newContinuationMismatchHandler(t, upstream, continuationMismatchAccounts(2, service.AccountTypeOAuth))
	response := serveContinuationMismatch(t, handler, strings.Replace(continuationMismatchBody, `"stream":false`, `"stream":true`, 1))

	require.Equal(t, []int64{1, 2}, upstream.accountIDs)
	require.Contains(t, response.body, "response.completed")
}

// When the accounts run out, the client receives what the first account
// answered, in that failure's own classification, and the session is where it
// was before the others were tried.
func TestOpenAIGatewayHandler_ContinuationCiphertextMismatchRendersTheFirstFailureWhenAccountsRunOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name          string
		first, second continuationMismatchReply
		code          string
	}{
		{"continuation_state_first", continuationMismatchUnrecoverable, continuationMismatchUndecryptable, service.OpenAIContinuationStateUnavailableCode},
		{"request_rejected_first", continuationMismatchUndecryptable, continuationMismatchUnrecoverable, service.OpenAIRequestRejectedCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &continuationMismatchUpstream{replies: map[int64]continuationMismatchReply{1: test.first, 2: test.second}}
			handler, cache := newContinuationMismatchHandler(t, upstream, continuationMismatchAccounts(2, service.AccountTypeAPIKey))
			response := serveContinuationMismatch(t, handler, continuationMismatchBody)

			require.Equal(t, []int64{1, 2}, upstream.accountIDs)
			require.Equal(t, http.StatusBadRequest, response.code)
			require.Equal(t, test.code, gjson.Get(response.body, "error.code").String())
			require.NotContains(t, response.body, "could not be decrypted")
			require.Equal(t, int64(1), continuationMismatchBoundAccount(t, cache))
		})
	}
}

// An account that read the ciphertext and was answering when its stream broke
// is where the conversation runs: the session stays with it.
func TestOpenAIGatewayHandler_ContinuationCiphertextMismatchKeepsTheSessionOnAnAccountThatWasAnswering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &continuationMismatchUpstream{
		rejection: continuationMismatchUnrecoverable,
		replies: map[int64]continuationMismatchReply{2: {status: http.StatusOK, stream: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\"}}\n\n" +
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial-answer\"}\n\n"}},
	}
	handler, cache := newContinuationMismatchHandler(t, upstream, continuationMismatchAccounts(3, service.AccountTypeAPIKey))
	response := serveContinuationMismatch(t, handler, strings.Replace(continuationMismatchBody, `"stream":false`, `"stream":true`, 1))

	require.Equal(t, []int64{1, 2}, upstream.accountIDs)
	require.Contains(t, response.body, "partial-answer")
	require.Equal(t, int64(2), continuationMismatchBoundAccount(t, cache))
}
