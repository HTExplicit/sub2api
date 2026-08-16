package admin

import (
	"context"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type CindyBalanceProbeHandler struct {
	probeService *service.CindyBalanceProbeService
}

func NewCindyBalanceProbeHandler(probeService *service.CindyBalanceProbeService) *CindyBalanceProbeHandler {
	return &CindyBalanceProbeHandler{probeService: probeService}
}

type cindyBalanceProbeFilterRequest struct {
	Platforms            []string `json:"platforms"`
	Types                []string `json:"types"`
	Statuses             []string `json:"statuses"`
	Plans                []string `json:"plans"`
	ProxyIDs             []int64  `json:"proxy_ids"`
	IncludeDirect        bool     `json:"include_direct"`
	FolderIDs            []int64  `json:"folder_ids"`
	IncludeUncategorized bool     `json:"include_uncategorized"`
	TagIDs               []int64  `json:"tag_ids"`
	AccountIDs           []int64  `json:"account_ids"`
	Search               string   `json:"search"`
	GroupID              int64    `json:"group_id"`
	PrivacyMode          string   `json:"privacy_mode"`
	CindyBalanceStatus   string   `json:"cindy_balance_status"`
	SortBy               string   `json:"sort_by"`
	SortOrder            string   `json:"sort_order"`
}

type cindyBalanceProbeScopeRequest struct {
	Mode       string                         `json:"mode" binding:"required"`
	AccountIDs []int64                        `json:"account_ids"`
	Filters    cindyBalanceProbeFilterRequest `json:"filters"`
}

type cindyBalanceProbePreviewRequest struct {
	Scope   cindyBalanceProbeScopeRequest `json:"scope" binding:"required"`
	RateRPS float64                       `json:"rate_rps"`
}

type cindyBalanceProbeCreateRequest struct {
	Scope                cindyBalanceProbeScopeRequest `json:"scope" binding:"required"`
	RateRPS              float64                       `json:"rate_rps"`
	ExpectedCount        int                           `json:"expected_count" binding:"min=0"`
	CandidateFingerprint string                        `json:"candidate_fingerprint" binding:"required"`
}

type cindyBalanceProbeRateRequest struct {
	RateRPS float64 `json:"rate_rps" binding:"required"`
}

func (h *CindyBalanceProbeHandler) Preview(c *gin.Context) {
	var req cindyBalanceProbePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("CINDY_BALANCE_PROBE_REQUEST_INVALID", "invalid Cindy balance probe request"))
		return
	}
	scope, err := normalizeCindyBalanceProbeScope(req.Scope)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	preview, err := h.probeService.Preview(c.Request.Context(), scope, req.RateRPS)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

func (h *CindyBalanceProbeHandler) Create(c *gin.Context) {
	var req cindyBalanceProbeCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("CINDY_BALANCE_PROBE_REQUEST_INVALID", "invalid Cindy balance probe request"))
		return
	}
	scope, err := normalizeCindyBalanceProbeScope(req.Scope)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return
	}
	requestedBy := subject.UserID
	job, err := h.probeService.CreateJob(
		c.Request.Context(), &requestedBy, scope, req.RateRPS, req.ExpectedCount, req.CandidateFingerprint,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, job)
}

func (h *CindyBalanceProbeHandler) Get(c *gin.Context) {
	jobID, ok := cindyBalanceProbeJobID(c)
	if !ok {
		return
	}
	job, err := h.probeService.GetJob(c.Request.Context(), jobID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, job)
}

// List returns a bounded recent-job view for progress restoration after reload.
func (h *CindyBalanceProbeHandler) List(c *gin.Context) {
	limit := parsePositiveCindyBalanceProbeQuery(c.Query("limit"), 10)
	jobs, err := h.probeService.ListJobs(c.Request.Context(), limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, jobs)
}

func (h *CindyBalanceProbeHandler) ListItems(c *gin.Context) {
	jobID, ok := cindyBalanceProbeJobID(c)
	if !ok {
		return
	}
	page := parsePositiveCindyBalanceProbeQuery(c.Query("page"), 1)
	pageSize := parsePositiveCindyBalanceProbeQuery(c.Query("page_size"), 50)
	items, err := h.probeService.ListItems(c.Request.Context(), jobID, c.Query("state"), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *CindyBalanceProbeHandler) SetRate(c *gin.Context) {
	jobID, ok := cindyBalanceProbeJobID(c)
	if !ok {
		return
	}
	var req cindyBalanceProbeRateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.ErrCindyBalanceProbeInvalidRate)
		return
	}
	job, err := h.probeService.SetRate(c.Request.Context(), jobID, req.RateRPS)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, job)
}

func (h *CindyBalanceProbeHandler) Pause(c *gin.Context) {
	h.mutate(c, h.probeService.Pause)
}

func (h *CindyBalanceProbeHandler) Resume(c *gin.Context) {
	h.mutate(c, h.probeService.Resume)
}

func (h *CindyBalanceProbeHandler) Cancel(c *gin.Context) {
	h.mutate(c, h.probeService.Cancel)
}

func (h *CindyBalanceProbeHandler) mutate(c *gin.Context, fn func(context.Context, int64) (*service.CindyBalanceProbeJob, error)) {
	jobID, ok := cindyBalanceProbeJobID(c)
	if !ok {
		return
	}
	job, err := fn(c.Request.Context(), jobID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, job)
}

func normalizeCindyBalanceProbeScope(scopeRequest cindyBalanceProbeScopeRequest) (service.CindyBalanceProbeScope, error) {
	mode := strings.ToLower(strings.TrimSpace(scopeRequest.Mode))
	filters := service.AccountConsoleFilters{CindyOnly: true, SortBy: "name", SortOrder: "asc"}
	switch mode {
	case "all":
	case "filter", "filtered", "current_filter":
		filters = scopeRequest.Filters.accountConsoleFilters()
		filters.CindyOnly = true
		mode = "filter"
	case "selected":
		if len(scopeRequest.AccountIDs) == 0 {
			return service.CindyBalanceProbeScope{}, infraerrors.BadRequest("CINDY_BALANCE_PROBE_SELECTION_EMPTY", "select at least one Cindy account")
		}
		filters.AccountIDs = append([]int64(nil), scopeRequest.AccountIDs...)
	default:
		return service.CindyBalanceProbeScope{}, infraerrors.BadRequest("CINDY_BALANCE_PROBE_SCOPE_INVALID", "scope mode must be all, filter, or selected")
	}
	return service.CindyBalanceProbeScope{Mode: mode, Filters: filters}, nil
}

func (r cindyBalanceProbeFilterRequest) accountConsoleFilters() service.AccountConsoleFilters {
	return service.AccountConsoleFilters{
		Platforms: append([]string(nil), r.Platforms...), Types: append([]string(nil), r.Types...),
		Statuses: append([]string(nil), r.Statuses...), Plans: append([]string(nil), r.Plans...),
		ProxyIDs: append([]int64(nil), r.ProxyIDs...), IncludeDirect: r.IncludeDirect,
		FolderIDs: append([]int64(nil), r.FolderIDs...), IncludeUncategorized: r.IncludeUncategorized,
		TagIDs: append([]int64(nil), r.TagIDs...), Search: strings.TrimSpace(r.Search), GroupID: r.GroupID,
		AccountIDs:  append([]int64(nil), r.AccountIDs...),
		PrivacyMode: strings.TrimSpace(r.PrivacyMode), CindyBalanceStatus: strings.TrimSpace(r.CindyBalanceStatus),
		SortBy: strings.TrimSpace(r.SortBy), SortOrder: strings.TrimSpace(r.SortOrder),
	}
}

func cindyBalanceProbeJobID(c *gin.Context) (int64, bool) {
	jobID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || jobID <= 0 {
		response.ErrorFrom(c, infraerrors.BadRequest("CINDY_BALANCE_PROBE_JOB_ID_INVALID", "invalid Cindy balance probe job id"))
		return 0, false
	}
	return jobID, true
}

func parsePositiveCindyBalanceProbeQuery(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
