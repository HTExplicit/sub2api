package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBatchTestSelectionsPersistByAccountIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body string
		want       map[int64]string
	}{
		{"individual", `{"items":[{"account_id":9,"model_id":"alias-a"},{"account_id":3,"model_id":"raw-B"},{"account_id":9,"model_id":"alias-a"}]}`, map[int64]string{9: "alias-a", 3: "raw-B"}},
		{"legacy", `{"account_ids":[9,3,9],"model_id":"shared"}`, map[int64]string{9: "shared", 3: "shared"}},
		{"legacy defaults", `{"account_ids":[9,3]}`, map[int64]string{9: "", 3: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &AccountHandler{}
			router := gin.New()
			repo := attachAccountJobSubmitter(router, h)
			router.POST("/batch-test", h.BatchTest)
			req := httptest.NewRequest(http.MethodPost, "/batch-test", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			setAccountJobTestIdempotencyKey(req)
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			require.Equal(t, http.StatusAccepted, out.Code, out.Body.String())
			var payload batchTestJobPayload
			params := requireSubmittedAccountJob(t, repo, service.AccountJobKindBatchTest, &payload)
			require.Len(t, params.Items, 2)
			ctx, cleanup, err := h.PrepareAccountJob(context.Background(), &service.AccountJob{Kind: service.AccountJobKindBatchTest}, json.RawMessage(params.PayloadCipher))
			require.NoError(t, err)
			defer cleanup()
			require.Equal(t, tc.want, ctx.Value(batchTestModelContextKey{}))
			// Failed-only retries keep the original payload but may renumber item ordinals.
			for _, seed := range params.Items {
				var metadata map[string]any
				require.NoError(t, json.Unmarshal(seed.Metadata, &metadata))
				require.Equal(t, tc.want[*seed.TargetAccountID], metadata["model_id"])
			}
		})
	}
}

func TestBatchTestRejectsAmbiguousSelections(t *testing.T) {
	for _, body := range []string{
		`{"items":[],"account_ids":[1]}`, `{"items":[{"account_id":1,"model_id":"a"}],"model_id":null}`,
		`{"items":[{"account_id":1,"model_id":"a"},{"account_id":1,"model_id":"b"}]}`,
		`{"items":[{"account_id":1,"model_id":"a","reasoning_effort":"low"},{"account_id":1,"model_id":"a","reasoning_effort":"high"}]}`,
		`{"items":[{"account_id":1,"model_id":" "}]}`, `{"items":[{"account_id":0,"model_id":"a"}]}`, `{"items":null}`, `{}`,
	} {
		var payload batchTestJobPayload
		require.NoError(t, json.Unmarshal([]byte(body), &payload))
		_, _, err := payload.normalize()
		require.Error(t, err, body)
	}
}

func TestBatchTestReasoningPersistsPerAccountForRetry(t *testing.T) {
	h := &AccountHandler{}
	router := gin.New()
	repo := attachAccountJobSubmitter(router, h)
	router.POST("/batch-test", h.BatchTest)
	req := httptest.NewRequest(http.MethodPost, "/batch-test", strings.NewReader(`{"items":[{"account_id":9,"model_id":"alias-a","reasoning_effort":"ultra"},{"account_id":3,"model_id":"other","reasoning_effort":"low"}]}`))
	req.Header.Set("Content-Type", "application/json")
	setAccountJobTestIdempotencyKey(req)
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	require.Equal(t, http.StatusAccepted, out.Code, out.Body.String())
	var payload batchTestJobPayload
	params := requireSubmittedAccountJob(t, repo, service.AccountJobKindBatchTest, &payload)
	want := map[int64]string{9: "ultra", 3: "low"}
	for _, item := range payload.Items {
		require.Equal(t, want[item.AccountID], item.ReasoningEffort)
	}
	for _, seed := range params.Items {
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(seed.Metadata, &metadata))
		require.Equal(t, want[*seed.TargetAccountID], metadata["reasoning_effort"])
	}
	// Restore and normalize the persisted payload as the failed-item retry path
	// does. Choices stay attached to account IDs despite changed item ordinals.
	_, _, err := payload.normalize()
	require.NoError(t, err)
	for _, item := range payload.Items {
		require.Equal(t, want[item.AccountID], item.ReasoningEffort)
	}
}

type batchCatalogAdmin struct {
	service.AdminService
	calls    atomic.Int32
	accounts []*service.Account
}

func (s *batchCatalogAdmin) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	s.calls.Add(1)
	return s.accounts, nil
}
func TestBatchTestCatalogUsesOneAccountReadAndSingleCatalogRules(t *testing.T) {
	svc := &batchCatalogAdmin{accounts: []*service.Account{{ID: 3, Name: "Claude", Platform: "anthropic", Type: "apikey"}, {ID: 9, Name: "Gemini", Platform: "gemini", Type: "apikey"}}}
	h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/models", h.BatchTestModels)
	req := httptest.NewRequest(http.MethodPost, "/models", strings.NewReader(`{"account_ids":[9,3,9,999]}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.EqualValues(t, 1, svc.calls.Load())
	var body struct {
		Data struct {
			Items []struct {
				AccountID int64           `json:"account_id"`
				Models    json.RawMessage `json:"models"`
				ErrorCode string          `json:"error_code"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 3)
	for i, account := range []*service.Account{svc.accounts[0], svc.accounts[1]} {
		models, err := h.accountTestModels(context.Background(), account)
		require.NoError(t, err)
		expected, err := json.Marshal(models)
		require.NoError(t, err)
		require.Equal(t, account.ID, body.Data.Items[i].AccountID)
		require.JSONEq(t, string(expected), string(body.Data.Items[i].Models))
	}
	require.Equal(t, "account_not_found", body.Data.Items[2].ErrorCode)
	ids := make([]int64, 101)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	encoded, _ := json.Marshal(map[string]any{"account_ids": ids})
	req = httptest.NewRequest(http.MethodPost, "/models", strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")
	out = httptest.NewRecorder()
	router.ServeHTTP(out, req)
	require.Equal(t, http.StatusBadRequest, out.Code)
	require.EqualValues(t, 1, svc.calls.Load())
}

type blockingBatchCatalogUpstream struct {
	service.HTTPUpstream
	active, peak atomic.Int32
	entered      chan struct{}
	release      chan struct{}
}

func (u *blockingBatchCatalogUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for old := u.peak.Load(); active > old; old = u.peak.Load() {
		if u.peak.CompareAndSwap(old, active) {
			break
		}
	}
	u.entered <- struct{}{}
	select {
	case <-u.release:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"mock-model"}]}`))}, nil
}
func TestBatchTestCatalogSharesFiveDiscoverySlotsAcrossRequests(t *testing.T) {
	upstream := &blockingBatchCatalogUpstream{entered: make(chan struct{}, 12), release: make(chan struct{})}
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	testSvc := service.NewAccountTestService(nil, nil, nil, nil, nil, upstream, cfg, nil)
	testSvc.SetOpenAIGatewayService(gateway)
	svc := &batchCatalogAdmin{}
	for i := int64(1); i <= 12; i++ {
		svc.accounts = append(svc.accounts, &service.Account{ID: i, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"base_url": fmt.Sprintf("https://catalog%d.example/v1", i), "api_key": "mock"}})
	}
	h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, testSvc, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/models", h.BatchTestModels)
	done := make(chan int, 2)
	for _, body := range []string{`{"account_ids":[1,2,3,4,5,6]}`, `{"account_ids":[7,8,9,10,11,12]}`} {
		go func(body string) {
			req := httptest.NewRequest("POST", "/models", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			done <- out.Code
		}(body)
	}
	released := false
	defer func() {
		if !released {
			close(upstream.release)
		}
	}()
	for i := 0; i < 5; i++ {
		select {
		case <-upstream.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("discovery did not start")
		}
	}
	select {
	case <-upstream.entered:
		t.Fatal("more than five concurrent discoveries")
	case <-time.After(25 * time.Millisecond):
	}
	close(upstream.release)
	released = true
	for i := 0; i < 2; i++ {
		select {
		case status := <-done:
			require.Equal(t, 200, status)
		case <-time.After(3 * time.Second):
			t.Fatal("discovery did not finish")
		}
	}
	require.EqualValues(t, 5, upstream.peak.Load())
}
