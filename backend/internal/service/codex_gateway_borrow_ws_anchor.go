package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	codexGatewayBorrowWSAnchorTTL   = time.Hour
	codexGatewayBorrowWSAnchorLimit = 1024
)

type codexGatewayBorrowWSAnchorKey struct {
	account, apiKey, group int64
	model, scope           string
	identity               [32]byte
}

type codexGatewayBorrowWSAnchorEntry struct {
	responseID, connID string
	expires            time.Time
}

type codexGatewayBorrowWSAnchorStore struct {
	mu      sync.Mutex
	entries map[codexGatewayBorrowWSAnchorKey]codexGatewayBorrowWSAnchorEntry
	busy    map[codexGatewayBorrowWSAnchorKey]bool
}

type codexGatewayBorrowWSTurn struct {
	store                            *codexGatewayBorrowWSAnchorStore
	key                              codexGatewayBorrowWSAnchorKey
	previousID, connID, scope, model string
	expires                          time.Time
	headers                          http.Header
	qualified                        bool
	finished                         sync.Once
}

// Passthrough owns a direct socket for the entire client session. Its anchor is
// local to that socket and can never be resumed by another direct dial.
type codexGatewayBorrowWSDirectSession struct {
	turn       *codexGatewayBorrowWSTurn
	mu         sync.Mutex
	responseID string
	valid      bool
}

func (session *codexGatewayBorrowWSDirectSession) check(c *gin.Context, account *Account, headers http.Header, rawClientBody []byte, model, proxyURL string) error {
	if session == nil || session.turn == nil {
		return nil
	}
	key, err := codexGatewayBorrowWSKey(c, account, headers, rawClientBody, model, proxyURL)
	if err != nil || key != session.turn.key || !time.Now().Before(session.turn.expires) {
		return NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	previous := strings.TrimSpace(gjson.GetBytes(rawClientBody, "previous_response_id").String())
	session.mu.Lock()
	defer session.mu.Unlock()
	if previous != "" && (!session.valid || previous != session.responseID) {
		return NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	return nil
}

func (session *codexGatewayBorrowWSDirectSession) completed(result *OpenAIForwardResult) {
	if session == nil || session.turn == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.valid = result != nil && isOpenAIWSSuccessTerminalEvent(result.UpstreamTerminalEvent) &&
		result.UpstreamResponseModel == session.turn.model && !result.UpstreamResponseModelConflict && result.RequestID != ""
	if session.valid {
		session.responseID = result.RequestID
	} else {
		session.responseID = ""
	}
}

func (session *codexGatewayBorrowWSDirectSession) close(pool *openAIWSConnPool) {
	if session == nil || session.turn == nil {
		return
	}
	turn := session.turn
	turn.finished.Do(func() {
		session.mu.Lock()
		responseID, valid := session.responseID, session.valid && time.Now().Before(turn.expires)
		session.mu.Unlock()
		store := turn.store
		store.mu.Lock()
		delete(store.busy, turn.key)
		previous := store.entries[turn.key]
		if valid {
			// Empty connID records a closed direct socket. The same bounded
			// anchor budget rejects reconnects without retaining that socket.
			store.entries[turn.key] = codexGatewayBorrowWSAnchorEntry{responseID: responseID, expires: turn.expires}
		}
		store.mu.Unlock()
		if valid && previous.connID != "" && pool != nil {
			pool.evictConn(turn.key.account, previous.connID)
		}
	})
}

// The HTTP template is only for eligibility and qualification. The real dial
// retains the existing WebSocket URL, account credentials and account proxy.
func codexGatewayBorrowWSRequest(ctx context.Context, wsURL string, headers http.Header) (*http.Request, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header = cloneHeader(headers)
	return req, nil
}

func (s *OpenAIGatewayService) codexGatewayBorrowWSSelected(account *Account, model string, req *http.Request) bool {
	if s == nil || s.gatewayBorrow == nil || account == nil || !account.IsOpenAIOAuthLike() ||
		!s.gatewayBorrow.ModelEligible(model) || !s.gatewayBorrow.RequestEligible(req) {
		return false
	}
	settings := s.gatewayBorrow.ConfigSnapshot()
	return settings.Enabled && slices.Contains(settings.TargetAccountIDs, account.ID)
}

func codexGatewayBorrowWSKey(c *gin.Context, account *Account, headers http.Header, rawClientBody []byte, model, proxyURL string) (codexGatewayBorrowWSAnchorKey, error) {
	apiKeyID := getAPIKeyIDFromContext(c)
	scope, _ := resolveOpenAIWSExecutionScope(c, rawClientBody, apiKeyID)
	if apiKeyID <= 0 || scope == "" {
		return codexGatewayBorrowWSAnchorKey{}, errors.New("codex gateway borrow websocket requires an authenticated API key and explicit session or thread identity")
	}
	// Turn state and cookie generations deliberately do not enter the key: a
	// response continuation remains on its original authenticated connection.
	identity := sha256.Sum256([]byte(headers.Get("Authorization") + "\x00" +
		headers.Get("Chatgpt-Account-Id") + "\x00" + account.GetCredential("access_token") + "\x00" + proxyURL))
	return codexGatewayBorrowWSAnchorKey{account: account.ID, apiKey: apiKeyID, group: getOpenAIGroupIDFromContext(c),
		model: strings.TrimSpace(model), scope: scope, identity: identity}, nil
}

func (store *codexGatewayBorrowWSAnchorStore) begin(key codexGatewayBorrowWSAnchorKey, previousID string, now time.Time) (*codexGatewayBorrowWSTurn, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.entries == nil {
		store.entries = make(map[codexGatewayBorrowWSAnchorKey]codexGatewayBorrowWSAnchorEntry)
		store.busy = make(map[codexGatewayBorrowWSAnchorKey]bool)
	}
	for k, entry := range store.entries {
		if !now.Before(entry.expires) && !store.busy[k] {
			// Expiring an anchor never clears an account pool or closes a socket.
			delete(store.entries, k)
		}
	}
	if store.busy[key] {
		return nil, errors.New("codex gateway borrow websocket session already has an active request")
	}
	entry, found := store.entries[key]
	previousID = strings.TrimSpace(previousID)
	if previousID != "" && (!found || entry.responseID != previousID || entry.connID == "" || !now.Before(entry.expires)) {
		return nil, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	if !found {
		occupied := len(store.entries)
		for busyKey := range store.busy {
			if _, exists := store.entries[busyKey]; !exists {
				occupied++
			}
		}
		if occupied >= codexGatewayBorrowWSAnchorLimit {
			return nil, errors.New("codex gateway borrow websocket anchor capacity reached")
		}
	}
	turn := &codexGatewayBorrowWSTurn{store: store, key: key, previousID: previousID, model: key.model,
		scope: fmt.Sprintf("%d:%d:%d:%s:%s:%x", key.account, key.apiKey, key.group, key.model, key.scope, key.identity)}
	if previousID != "" {
		turn.connID = entry.connID
		turn.expires = entry.expires
	} else {
		turn.expires = now.Add(codexGatewayBorrowWSAnchorTTL)
	}
	store.busy[key] = true
	return turn, nil
}

func (store *codexGatewayBorrowWSAnchorStore) knowsContinuation(key codexGatewayBorrowWSAnchorKey, previousID string) (exact, knownResponse bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, exact = store.entries[key]
	if previousID != "" {
		for storedKey, entry := range store.entries {
			if storedKey.account == key.account && entry.responseID == previousID {
				knownResponse = true
				break
			}
		}
	}
	return exact, knownResponse
}

func (s *OpenAIGatewayService) prepareCodexGatewayBorrowWSTurn(ctx context.Context, c *gin.Context, account *Account,
	wsURL string, headers http.Header, rawClientBody []byte, model, previousID, proxyURL string, executionScope ...string) (*codexGatewayBorrowWSTurn, error) {
	if s == nil || s.gatewayBorrow == nil || account == nil {
		return nil, nil
	}
	req, err := codexGatewayBorrowWSRequest(ctx, wsURL, headers)
	if err != nil {
		return nil, err
	}
	selected := s.codexGatewayBorrowWSSelected(account, model, req)
	key, err := codexGatewayBorrowWSKey(c, account, headers, rawClientBody, model, proxyURL)
	if err != nil {
		if !selected {
			_, knownResponse := s.gatewayBorrow.wsAnchors.knowsContinuation(codexGatewayBorrowWSAnchorKey{account: account.ID}, previousID)
			if knownResponse {
				return nil, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
			}
			return nil, nil
		}
		return nil, err
	}
	if len(executionScope) > 0 && strings.TrimSpace(executionScope[0]) != "" {
		key.scope = strings.TrimSpace(executionScope[0])
	}
	// Existing conversations survive a setting edit or disable. Only new roots
	// require the current configuration and a current shared cookie.
	knownAnchor := false
	if previousID != "" {
		var knownResponse bool
		knownAnchor, knownResponse = s.gatewayBorrow.wsAnchors.knowsContinuation(key, previousID)
		if !knownAnchor && knownResponse {
			// A model, credential or authenticated client change cannot convert
			// an existing borrowed response into an ordinary reconnect.
			return nil, NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
		}
	}
	if !selected && !knownAnchor {
		return nil, nil
	}
	turn, err := s.gatewayBorrow.wsAnchors.begin(key, previousID, time.Now())
	if err != nil {
		return nil, err
	}
	if turn.previousID != "" {
		// No cookie reacquisition and no source or target probe on continuation.
		turn.headers = cloneHeader(headers)
		return turn, nil
	}
	borrowedReq, application, err := s.gatewayBorrow.Apply(req, account, model, proxyURL, nil, false)
	if err != nil {
		turn.finish(s.getOpenAIWSConnPool(), nil, err)
		return nil, err
	}
	if application == nil || !application.Applied || borrowedReq == nil {
		failure := &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowChanged}
		turn.finish(s.getOpenAIWSConnPool(), nil, failure)
		return nil, failure
	}
	turn.headers = borrowedReq.Header
	return turn, nil
}

func codexGatewayBorrowWSPreparationError(c *gin.Context, account *Account, err error) error {
	if !IsCodexGatewayBorrowFailure(err) {
		return err
	}
	RecordCodexGatewayBorrowPreparationFailure(c, account, err)
	return NewCodexGatewayBorrowRequestFailure(err)
}

func (turn *codexGatewayBorrowWSTurn) applyToAcquire(req *openAIWSAcquireRequest) {
	if turn == nil || req == nil {
		return
	}
	req.AnchorScope = turn.scope
	req.Headers = cloneHeader(turn.headers)
	if turn.previousID == "" {
		req.PreferredConnID = ""
		req.ForcePreferredConn = false
		req.ForceNewConn = true
	} else {
		req.PreferredConnID = turn.connID
		req.ForcePreferredConn = true
		req.ForceNewConn = false
	}
}

func (turn *codexGatewayBorrowWSTurn) bindLease(lease *openAIWSConnLease) error {
	if turn == nil {
		return nil
	}
	if lease == nil || lease.conn == nil || (turn.previousID != "" && turn.connID != lease.ConnID()) {
		return NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	turn.connID = lease.ConnID()
	lease.conn.anchorUntilNano.Store(turn.expires.UnixNano())
	return nil
}

func (turn *codexGatewayBorrowWSTurn) observe(message []byte) {
	if turn == nil || gjson.GetBytes(message, "type").String() != "response.completed" {
		return
	}
	response := gjson.GetBytes(message, "response")
	turn.qualified = strings.TrimSpace(response.Get("model").String()) == turn.model &&
		response.Get("status").String() == "completed" && !response.Get("error").IsObject()
}

func (turn *codexGatewayBorrowWSTurn) finish(pool *openAIWSConnPool, result *OpenAIForwardResult, resultErr error) {
	if turn == nil || turn.store == nil {
		return
	}
	turn.finished.Do(func() {
		store := turn.store
		store.mu.Lock()
		delete(store.busy, turn.key)
		previous := store.entries[turn.key]
		responseID := ""
		if result != nil {
			responseID = firstNonEmpty(result.ResponseID, result.RequestID)
		}
		success := resultErr == nil && result != nil && !result.ClientDisconnect && turn.qualified && responseID != "" &&
			result.UpstreamResponseModel == turn.model && !result.UpstreamResponseModelConflict && time.Now().Before(turn.expires)
		if success {
			store.entries[turn.key] = codexGatewayBorrowWSAnchorEntry{responseID: responseID, connID: turn.connID, expires: turn.expires}
		} else if turn.previousID != "" {
			delete(store.entries, turn.key)
		}
		store.mu.Unlock()
		if success && turn.previousID == "" && previous.connID != "" && previous.connID != turn.connID && pool != nil {
			pool.evictConn(turn.key.account, previous.connID)
		}
		if !success && turn.connID != "" && pool != nil {
			pool.evictConn(turn.key.account, turn.connID)
		}
	})
}
