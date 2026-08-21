package admin

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountJobHandler struct{ jobs *service.AccountJobService }

func NewAccountJobHandler(jobs *service.AccountJobService) *AccountJobHandler {
	return &AccountJobHandler{jobs: jobs}
}

func (h *AccountJobHandler) List(c *gin.Context) {
	page := positiveAccountJobQuery(c.Query("page"), 1)
	pageSize := positiveAccountJobQuery(c.Query("page_size"), 20)
	jobs, err := h.jobs.List(c.Request.Context(), 0, c.Query("kind"), c.Query("status"), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, accountJobHTTPError(err))
		return
	}
	response.Success(c, jobs)
}

func (h *AccountJobHandler) Get(c *gin.Context) {
	jobID, ok := accountJobPathID(c)
	if !ok {
		return
	}
	job, err := h.jobs.Get(c.Request.Context(), jobID)
	if err != nil {
		response.ErrorFrom(c, accountJobHTTPError(err))
		return
	}
	response.Success(c, job)
}

func (h *AccountJobHandler) ListItems(c *gin.Context) {
	jobID, ok := accountJobPathID(c)
	if !ok {
		return
	}
	page := positiveAccountJobQuery(c.Query("page"), 1)
	pageSize := positiveAccountJobQuery(c.Query("page_size"), 50)
	items, err := h.jobs.ListItems(c.Request.Context(), jobID, c.Query("status"), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, accountJobHTTPError(err))
		return
	}
	response.Success(c, items)
}

func (h *AccountJobHandler) Cancel(c *gin.Context) {
	jobID, ok := accountJobPathID(c)
	if !ok {
		return
	}
	actorID, ok := accountJobActorID(c)
	if !ok {
		return
	}
	job, err := h.jobs.Cancel(c.Request.Context(), jobID, actorID)
	if err != nil {
		response.ErrorFrom(c, accountJobHTTPError(err))
		return
	}
	response.Success(c, job)
}

func (h *AccountJobHandler) RetryFailed(c *gin.Context) {
	jobID, ok := accountJobPathID(c)
	if !ok {
		return
	}
	actorID, ok := accountJobActorID(c)
	if !ok {
		return
	}
	job, replayed, err := h.jobs.RetryFailed(c.Request.Context(), jobID, actorID, c.GetHeader("Idempotency-Key"))
	if err != nil {
		response.ErrorFrom(c, accountJobHTTPError(err))
		return
	}
	if replayed {
		c.Header("Idempotency-Replayed", "true")
	}
	response.Accepted(c, job)
}

func accountJobActorID(c *gin.Context) (int64, bool) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return 0, false
	}
	return subject.UserID, true
}

func accountJobPathID(c *gin.Context) (int64, bool) {
	jobID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || jobID <= 0 {
		response.ErrorFrom(c, infraerrors.BadRequest("ACCOUNT_JOB_ID_INVALID", "invalid account job id"))
		return 0, false
	}
	return jobID, true
}

func positiveAccountJobQuery(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func accountJobHTTPError(err error) error {
	switch {
	case errors.Is(err, service.ErrAccountJobIdempotencyRequired):
		return infraerrors.BadRequest("ACCOUNT_JOB_IDEMPOTENCY_REQUIRED", "Idempotency-Key is required")
	case errors.Is(err, service.ErrAccountJobIdempotencyConflict):
		return infraerrors.Conflict("ACCOUNT_JOB_IDEMPOTENCY_CONFLICT", "Idempotency-Key was reused with a different request")
	case errors.Is(err, service.ErrAccountJobBatchTooLarge):
		return infraerrors.BadRequest("ACCOUNT_JOB_BATCH_TOO_LARGE", "account jobs support at most 100 items")
	case errors.Is(err, service.ErrAccountJobNotFound):
		return infraerrors.NotFound("ACCOUNT_JOB_NOT_FOUND", "account job not found")
	case errors.Is(err, service.ErrAccountJobPayloadExpired):
		return infraerrors.New(410, "ACCOUNT_JOB_PAYLOAD_EXPIRED", "account job payload has expired")
	case errors.Is(err, service.ErrAccountJobNotRetryable):
		return infraerrors.Conflict("ACCOUNT_JOB_NOT_RETRYABLE", "account job has no failed items to retry")
	case errors.Is(err, service.ErrAccountJobInvalidMetadata):
		return infraerrors.BadRequest("ACCOUNT_JOB_METADATA_REJECTED", "account job metadata must not contain credentials")
	default:
		return err
	}
}

type accountIDsJobPayload struct {
	AccountIDs []int64 `json:"account_ids"`
}

type batchCreateJobPayload struct {
	Accounts []CreateAccountRequest `json:"accounts"`
}

func (h *AccountHandler) SetAccountJobService(jobs *service.AccountJobService) {
	h.accountJobs = jobs
}

func (h *AccountHandler) submitAccountJob(c *gin.Context, kind string, payload any, seeds []service.AccountJobItemSeed) {
	if h == nil || h.accountJobs == nil {
		response.ErrorFrom(c, infraerrors.New(503, "ACCOUNT_JOBS_UNAVAILABLE", "account jobs are unavailable"))
		return
	}
	actorID, ok := accountJobActorID(c)
	if !ok {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("ACCOUNT_JOB_PAYLOAD_INVALID", "invalid account job payload"))
		return
	}
	metadata, _ := json.Marshal(map[string]any{"target_count": len(seeds)})
	job, replayed, err := h.accountJobs.Submit(c.Request.Context(), actorID, kind, c.GetHeader("Idempotency-Key"), raw, metadata, seeds)
	if err != nil {
		response.ErrorFrom(c, accountJobHTTPError(err))
		return
	}
	if replayed {
		c.Header("Idempotency-Replayed", "true")
	}
	response.Accepted(c, job)
}

func accountJobSeeds(ids []int64) []service.AccountJobItemSeed {
	seeds := make([]service.AccountJobItemSeed, 0, len(ids))
	for index, id := range ids {
		accountID := id
		seeds = append(seeds, service.AccountJobItemSeed{
			Ordinal: index + 1, TargetAccountID: &accountID, Metadata: json.RawMessage(`{}`),
		})
	}
	return seeds
}

func ordinalAccountJobSeeds(count int) []service.AccountJobItemSeed {
	seeds := make([]service.AccountJobItemSeed, 0, count)
	for index := 0; index < count; index++ {
		seeds = append(seeds, service.AccountJobItemSeed{Ordinal: index + 1, Metadata: json.RawMessage(`{}`)})
	}
	return seeds
}

func (h *AccountHandler) submitAccountIDsJob(c *gin.Context, kind string, ids []int64) {
	ids = normalizeInt64IDList(ids)
	if len(ids) == 0 {
		response.BadRequest(c, "account_ids is required")
		return
	}
	h.submitAccountJob(c, kind, accountIDsJobPayload{AccountIDs: ids}, accountJobSeeds(ids))
}

func (h *AccountHandler) submitOneAccountJob(c *gin.Context, kind string, payload any) {
	h.submitAccountJob(c, kind, payload, ordinalAccountJobSeeds(1))
}

func accountJobSucceeded(itemID int64, metadata any) service.AccountJobExecutionResult {
	raw, _ := json.Marshal(metadata)
	return service.AccountJobExecutionResult{ItemID: itemID, Status: service.AccountJobItemStatusSucceeded, Metadata: raw}
}

func accountJobFailed(itemID int64, code string) service.AccountJobExecutionResult {
	return service.AccountJobExecutionResult{ItemID: itemID, Status: service.AccountJobItemStatusFailed,
		Metadata: json.RawMessage(`{}`), ErrorCode: code, ErrorMessage: "account job item failed"}
}
