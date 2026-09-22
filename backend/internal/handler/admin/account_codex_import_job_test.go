package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountJobCodexImportSeedsEveryFlattenedEntry(t *testing.T) {
	tokens := []string{
		buildCodexAccessToken(t, "job-workspace", "job-user-1", time.Now().Add(time.Hour)),
		buildCodexAccessToken(t, "job-workspace", "job-user-2", time.Now().Add(time.Hour)),
		buildCodexAccessToken(t, "job-workspace", "job-user-3", time.Now().Add(time.Hour)),
	}
	nested, err := json.Marshal([]any{tokens[0], []any{tokens[1], tokens[2]}})
	require.NoError(t, err)
	object, err := json.Marshal(map[string]any{"access_token": tokens[0]})
	require.NoError(t, err)
	last, err := json.Marshal([]string{tokens[2]})
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		req  CodexSessionImportRequest
		want int
	}{
		{name: "nested_array", req: CodexSessionImportRequest{Content: string(nested)}, want: 3},
		{name: "mixed_jsonl_and_raw_tokens", req: CodexSessionImportRequest{Content: string(object) + "\n" + tokens[1] + "\n" + string(last)}, want: 3},
		{name: "blank_contents_do_not_seed_items", req: CodexSessionImportRequest{Content: tokens[0], Contents: []string{" \t\n", string(last), "", "[]"}}, want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newCodexImportMemoryAdminService(nil)
			handler := &AccountHandler{adminService: svc}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			repo := attachAccountJobSubmitter(router, handler)
			router.POST("/accounts/import/codex-session", handler.ImportCodexSession)
			recorder := submitCodexImportJobTestRequest(t, router, test.req, "flattened-entry-fixture")
			require.Equal(t, http.StatusAccepted, recorder.Code)
			require.Equal(t, 1, len(repo.created))
			params := repo.created[0]
			require.Equal(t, service.AccountJobKindImportCodex, params.Kind)
			if got := len(params.Items); got != test.want {
				t.Errorf("seed count = %d, want %d flattened entries", got, test.want)
			}
			results := executeCodexImportJobTestItems(t, handler, params)
			succeeded := 0
			for index, result := range results {
				if result.Status != service.AccountJobItemStatusSucceeded {
					t.Errorf("item %d status = %q, code = %q", index+1, result.Status, result.ErrorCode)
					continue
				}
				succeeded++
				var metadata map[string]any
				require.NoError(t, json.Unmarshal(result.Metadata, &metadata))
				require.Equal(t, float64(index+1), metadata["source_index"])
				require.Equal(t, "created", metadata["action"])
				require.Positive(t, metadata["account_id"])
			}
			require.Equal(t, test.want, succeeded, "every flattened entry must have a successful job item")
			require.Equal(t, test.want, len(svc.createdAccounts))
			require.Empty(t, svc.updatedAccounts)
			for _, token := range tokens {
				if strings.Contains(recorder.Body.String(), token) || strings.Contains(string(params.Metadata), token) {
					t.Fatal("synthetic credential leaked outside the encrypted payload")
				}
				for _, result := range results {
					if strings.Contains(string(result.Metadata)+result.ErrorMessage, token) {
						t.Fatal("synthetic credential leaked into an item result")
					}
				}
			}
		})
	}
}

func TestAccountJobCodexImportRejectsEmptyOrMalformedContainers(t *testing.T) {
	const canary = "synthetic-import-secret-canary"
	for _, test := range []struct {
		name string
		req  CodexSessionImportRequest
	}{
		{name: "empty_request"},
		{name: "whitespace_only", req: CodexSessionImportRequest{Content: " \t", Contents: []string{"", "\n "}}},
		{name: "empty_arrays", req: CodexSessionImportRequest{Content: "[[],[]]", Contents: []string{"[]"}}},
		{name: "malformed_json", req: CodexSessionImportRequest{Content: `{"access_token":"` + canary + `"`}},
		{name: "bad_later_content", req: CodexSessionImportRequest{Content: canary, Contents: []string{`["` + canary + `"`}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newCodexImportMemoryAdminService(nil)
			handler := &AccountHandler{adminService: svc}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			repo := attachAccountJobSubmitter(router, handler)
			router.POST("/accounts/import/codex-session", handler.ImportCodexSession)
			recorder := submitCodexImportJobTestRequest(t, router, test.req, "invalid-container-fixture")
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, 0, len(repo.created))
			require.Equal(t, 0, svc.lastListAccounts.calls)
			require.Equal(t, 0, len(svc.createdAccounts))
			require.Equal(t, 0, len(svc.updatedAccounts))
			if strings.Contains(recorder.Body.String(), canary) {
				t.Fatal("synthetic credential leaked into a parse error")
			}
		})
	}
}

func TestAccountJobCodexImportRejectsMalformedEnvelopeWithoutEcho(t *testing.T) {
	const canary = "synthetic-envelope-secret-canary"
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "truncated_json", body: `{"content":"` + canary + `"`},
		{name: "wrong_content_type", body: `{"content":{"access_token":"` + canary + `"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newCodexImportMemoryAdminService(nil)
			handler := &AccountHandler{adminService: svc}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			repo := attachAccountJobSubmitter(router, handler)
			router.POST("/accounts/import/codex-session", handler.ImportCodexSession)
			request := httptest.NewRequest(http.MethodPost, "/accounts/import/codex-session", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "invalid-envelope-fixture")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "Invalid Codex session import request")
			require.Equal(t, 0, len(repo.created))
			require.Equal(t, 0, svc.lastListAccounts.calls)
			if strings.Contains(recorder.Body.String(), canary) {
				t.Fatal("synthetic credential leaked through request binding")
			}
		})
	}
}

func TestAccountJobCodexImportKeepsEntryFailuresLocal(t *testing.T) {
	const canary = "synthetic-invalid-entry-secret"
	first := buildCodexAccessToken(t, "job-workspace", "first-valid-user", time.Now().Add(time.Hour))
	last := buildCodexAccessToken(t, "job-workspace", "last-valid-user", time.Now().Add(time.Hour))
	content, err := json.Marshal([]any{first, map[string]any{"password": canary}, last})
	require.NoError(t, err)
	svc := newCodexImportMemoryAdminService(nil)
	handler := &AccountHandler{adminService: svc}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	repo := attachAccountJobSubmitter(router, handler)
	router.POST("/accounts/import/codex-session", handler.ImportCodexSession)
	recorder := submitCodexImportJobTestRequest(t, router, CodexSessionImportRequest{Content: string(content)}, "item-failure-fixture")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	params := repo.created[0]
	require.Equal(t, 3, len(params.Items))
	results := executeCodexImportJobTestItems(t, handler, params)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	require.Equal(t, service.AccountJobItemStatusFailed, results[1].Status)
	require.Equal(t, "import_failed", results[1].ErrorCode)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[2].Status)
	require.Equal(t, 2, len(svc.createdAccounts))
	require.Equal(t, 0, len(svc.updatedAccounts))
	for _, result := range results {
		if strings.Contains(string(result.Metadata)+result.ErrorMessage, canary) {
			t.Fatal("synthetic credential leaked into an item failure")
		}
	}
	for _, ordinal := range []int{-1, 0, 4} {
		result, executeErr := handler.ExecuteAccountJob(context.Background(), &service.AccountJob{Kind: params.Kind}, json.RawMessage(params.PayloadCipher), []service.AccountJobItem{{ID: 99, Ordinal: ordinal}})
		require.NoError(t, executeErr)
		require.Equal(t, 1, len(result))
		require.Equal(t, "payload_invalid", result[0].ErrorCode)
	}
	require.Equal(t, 2, len(svc.createdAccounts), "out-of-range ordinals must not mutate accounts")
}

func TestAccountJobCodexImportPreservesReplayRetryAndPayloadBoundary(t *testing.T) {
	tokens := []string{
		buildCodexAccessToken(t, "job-workspace", "retry-user-1", time.Now().Add(time.Hour)),
		buildCodexAccessToken(t, "job-workspace", "retry-user-2", time.Now().Add(time.Hour)),
		buildCodexAccessToken(t, "job-workspace", "retry-user-3", time.Now().Add(time.Hour)),
	}
	content, err := json.Marshal(tokens)
	require.NoError(t, err)
	req := CodexSessionImportRequest{Content: string(content), Contents: []string{" \n"}}
	svc := &codexImportJobTransientAdmin{
		codexImportMemoryAdminService: newCodexImportMemoryAdminService(nil),
		failToken:                     tokens[1],
	}
	handler := &AccountHandler{adminService: svc}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	repo := &codexImportJobReplayRepository{accountJobSubmitRepository: attachAccountJobSubmitter(router, handler)}
	cipher := &codexImportJobOpaqueCipher{}
	jobs := service.NewAccountJobService(repo, cipher)
	handler.SetAccountJobService(jobs)
	router.POST("/accounts/import/codex-session", handler.ImportCodexSession)
	submittedAt := time.Now().UTC()
	recorder := submitCodexImportJobTestRequest(t, router, req, "replay-fixture")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	params := repo.created[0]
	require.Equal(t, 3, len(params.Items))
	require.Equal(t, 24*time.Hour, service.AccountJobPayloadTTL)
	require.Equal(t, 24*time.Hour, service.AccountJobResultTTL)
	require.WithinDuration(t, submittedAt.Add(24*time.Hour), params.PayloadExpires, time.Second)
	var savedRequest CodexSessionImportRequest
	require.NoError(t, json.Unmarshal([]byte(cipher.plaintext), &savedRequest))
	if savedRequest.Content != req.Content || len(savedRequest.Contents) != 1 || savedRequest.Contents[0] != req.Contents[0] {
		t.Fatal("submission changed the payload instead of preserving its idempotency input")
	}
	for _, token := range tokens {
		if strings.Contains(params.PayloadCipher+string(params.Metadata)+recorder.Body.String(), token) {
			t.Fatal("synthetic credential escaped the cipher boundary")
		}
	}
	recorder = submitCodexImportJobTestRequest(t, router, req, "replay-fixture")
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "true", recorder.Header().Get("Idempotency-Replayed"))
	require.Equal(t, 1, len(repo.created))
	changed := req
	changed.Name = "different-request"
	recorder = submitCodexImportJobTestRequest(t, router, changed, "replay-fixture")
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, 1, len(repo.created))
	require.Equal(t, 1, cipher.encryptions)

	plaintextParams := params
	plaintextParams.PayloadCipher, err = cipher.Decrypt(params.PayloadCipher)
	require.NoError(t, err)
	results := executeCodexImportJobTestItems(t, handler, plaintextParams)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[0].Status)
	require.Equal(t, service.AccountJobItemStatusFailed, results[1].Status)
	require.Equal(t, "import_failed", results[1].ErrorCode)
	require.Equal(t, service.AccountJobItemStatusSucceeded, results[2].Status)
	require.Equal(t, 2, len(svc.createdAccounts))
	require.Equal(t, 3, svc.createAttempts)
	if strings.Contains(results[1].ErrorMessage+string(results[1].Metadata), tokens[1]) {
		t.Fatal("synthetic backend error leaked a credential into the job result")
	}
	repo.failedSeeds = []service.AccountJobItemSeed{params.Items[1]}
	retry, replayed, err := jobs.RetryFailed(context.Background(), 1, accountJobTestActorID, "retry-fixture")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, 1, retry.TargetCount)
	require.Equal(t, 2, len(repo.created))
	retryParams := repo.created[1]
	require.Equal(t, 2, retryParams.Items[0].Ordinal, "retry must keep the original flattened ordinal")
	require.Equal(t, params.PayloadCipher, retryParams.PayloadCipher)
	require.Equal(t, params.PayloadExpires, retryParams.PayloadExpires, "retry must not extend the original payload lifetime")
	plaintextParams = retryParams
	plaintextParams.PayloadCipher, err = cipher.Decrypt(retryParams.PayloadCipher)
	require.NoError(t, err)
	retryResults := executeCodexImportJobTestItems(t, handler, plaintextParams)
	require.Equal(t, 1, len(retryResults))
	require.Equal(t, service.AccountJobItemStatusSucceeded, retryResults[0].Status)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(retryResults[0].Metadata, &metadata))
	require.Equal(t, float64(2), metadata["source_index"])
	require.Equal(t, 3, len(svc.createdAccounts))
	require.Equal(t, 4, svc.createAttempts, "retry must not execute either previously successful entry")
	require.Equal(t, 0, len(svc.updatedAccounts))
	_, replayed, err = jobs.RetryFailed(context.Background(), 1, accountJobTestActorID, "retry-fixture")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, 2, len(repo.created))
	require.Equal(t, 1, cipher.encryptions)
}

type codexImportJobTransientAdmin struct {
	*codexImportMemoryAdminService
	failToken      string
	failed         bool
	createAttempts int
}

func (s *codexImportJobTransientAdmin) CreateAccount(ctx context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	s.createAttempts++
	if input.Credentials["access_token"] == s.failToken && !s.failed {
		s.failed = true
		return nil, errors.New("synthetic backend failure: " + s.failToken)
	}
	return s.codexImportMemoryAdminService.CreateAccount(ctx, input)
}

type codexImportJobOpaqueCipher struct {
	plaintext   string
	encryptions int
}

func (c *codexImportJobOpaqueCipher) Encrypt(plaintext string) (string, error) {
	c.plaintext = plaintext
	c.encryptions++
	return "opaque-codex-job-fixture", nil
}

func (c *codexImportJobOpaqueCipher) Decrypt(ciphertext string) (string, error) {
	if ciphertext != "opaque-codex-job-fixture" {
		return "", errors.New("invalid synthetic cipher")
	}
	return c.plaintext, nil
}

type codexImportJobReplayRepository struct {
	*accountJobSubmitRepository
	failedSeeds []service.AccountJobItemSeed
}

func (r *codexImportJobReplayRepository) FindIdempotent(_ context.Context, actor int64, kind, key string) (*service.AccountJob, error) {
	for index, params := range r.created {
		if params.CreatedBy == actor && params.Kind == kind && params.IdempotencyKey == key {
			return &service.AccountJob{
				ID: int64(index + 1), CreatedBy: actor, Kind: kind,
				IdempotencyKey: key, RequestHash: params.RequestHash, Metadata: params.Metadata,
				TargetCount: len(params.Items), Attempt: params.Attempt, RetryOfJobID: params.RetryOfJobID,
			}, nil
		}
	}
	return nil, service.ErrAccountJobNotFound
}

func (r *codexImportJobReplayRepository) FailedItemSeeds(ctx context.Context, id, actor int64) (*service.AccountJob, []service.AccountJobItemSeed, string, time.Time, error) {
	if id != 1 || len(r.created) == 0 || r.created[0].CreatedBy != actor {
		return nil, nil, "", time.Time{}, service.ErrAccountJobNotFound
	}
	params := r.created[0]
	job, err := r.FindIdempotent(ctx, actor, params.Kind, params.IdempotencyKey)
	return job, append([]service.AccountJobItemSeed(nil), r.failedSeeds...), params.PayloadCipher, params.PayloadExpires, err
}

func submitCodexImportJobTestRequest(t *testing.T, router *gin.Engine, req CodexSessionImportRequest, key string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/accounts/import/codex-session", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func executeCodexImportJobTestItems(t *testing.T, handler *AccountHandler, params service.CreateAccountJobParams) []service.AccountJobExecutionResult {
	t.Helper()
	job := &service.AccountJob{ID: 1, Kind: params.Kind, TargetCount: len(params.Items)}
	raw := json.RawMessage(params.PayloadCipher) // Existing submission fixture deliberately uses a no-op cipher.
	ctx, cleanup, err := handler.PrepareAccountJob(context.Background(), job, raw)
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}
	results := make([]service.AccountJobExecutionResult, 0, len(params.Items))
	for index, seed := range params.Items {
		result, executeErr := handler.ExecuteAccountJob(ctx, job, raw, []service.AccountJobItem{{
			ID: int64(index + 1), Ordinal: seed.Ordinal, Action: seed.Action,
			TargetAccountID: seed.TargetAccountID, Metadata: seed.Metadata,
		}})
		require.NoError(t, executeErr)
		require.Equal(t, 1, len(result))
		results = append(results, result[0])
	}
	return results
}
