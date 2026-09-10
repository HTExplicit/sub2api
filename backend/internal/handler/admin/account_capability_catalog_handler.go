package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountCapabilityCatalogHandler struct {
	catalog accountCapabilityCatalogReader
}

type accountCapabilityCatalogReader interface {
	Candidates(context.Context, service.AccountCapabilityCandidateFilter) (*service.AccountCapabilityCandidatePage, error)
	Overview(context.Context, service.AccountCapabilityOverviewFilter) (*service.AccountCapabilityOverview, error)
	Recommend(context.Context, service.AccountCapabilityRecommendationRequest) (*service.AccountCapabilityRecommendation, error)
}

func NewAccountCapabilityCatalogHandler(catalog *service.AccountCapabilityCatalogService) *AccountCapabilityCatalogHandler {
	return &AccountCapabilityCatalogHandler{catalog: catalog}
}

func (h *AccountCapabilityCatalogHandler) Candidates(c *gin.Context) {
	folderIDs, ok := capabilityCatalogQueryIDs(c.Query("folder_ids"))
	if !ok || len(folderIDs) == 0 {
		response.BadRequest(c, "folder_ids must contain positive folder IDs")
		return
	}
	accountIDs, ok := capabilityCatalogQueryIDs(c.Query("account_ids"))
	if !ok {
		response.BadRequest(c, "account_ids must contain positive account IDs")
		return
	}
	groupIDs, ok := capabilityCatalogQueryIDs(c.Query("group_ids"))
	if !ok {
		response.BadRequest(c, "group_ids must contain positive group IDs")
		return
	}
	result, err := h.catalog.Candidates(c.Request.Context(), service.AccountCapabilityCandidateFilter{
		FolderIDs: folderIDs, AccountIDs: accountIDs, GroupIDs: groupIDs, Search: c.Query("search"), Status: c.Query("status"),
		Page: positiveAccountJobQuery(c.Query("page"), 1), PageSize: positiveAccountJobQuery(c.Query("page_size"), 50),
	})
	if capabilityCatalogResponseError(c, err) {
		return
	}
	response.Success(c, result)
}

// Overview and Plan are read-only. They never discover, probe, reserve a group
// identifier, or create a change set simply because the page was opened.
func (h *AccountCapabilityCatalogHandler) Overview(c *gin.Context) {
	folderIDs, foldersOK := capabilityCatalogQueryIDs(c.Query("folder_ids"))
	accountIDs, accountsOK := capabilityCatalogQueryIDs(c.Query("account_ids"))
	groupIDs, groupsOK := capabilityCatalogQueryIDs(c.Query("group_ids"))
	if !foldersOK || !accountsOK || !groupsOK {
		response.BadRequest(c, "Source and group IDs must be positive numbers")
		return
	}
	result, err := h.catalog.Overview(c.Request.Context(), service.AccountCapabilityOverviewFilter{
		FolderIDs: folderIDs, AccountIDs: accountIDs, GroupIDs: groupIDs, Search: c.Query("search"),
	})
	if capabilityCatalogResponseError(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *AccountCapabilityCatalogHandler) Plan(c *gin.Context) {
	var request service.AccountCapabilityRecommendationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid public model recommendation request")
		return
	}
	result, err := h.catalog.Recommend(c.Request.Context(), request)
	if capabilityCatalogResponseError(c, err) {
		return
	}
	response.Success(c, result)
}

func capabilityCatalogResponseError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, service.ErrAccountCapabilityInvalid) || errors.Is(err, service.ErrAccountCapabilityScope) {
		response.BadRequest(c, "The selected source or model is unavailable; refresh the public model overview")
		return true
	}
	if errors.Is(err, service.ErrAccountCapabilityConflict) {
		response.Error(c, http.StatusConflict, "The source configuration changed; refresh the recommendation")
		return true
	}
	return response.ErrorFrom(c, err)
}

func capabilityCatalogQueryIDs(raw string) ([]int64, bool) {
	result := []int64{}
	if strings.TrimSpace(raw) == "" {
		return result, true
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 1000 {
		return nil, false
	}
	seen := map[int64]bool{}
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, true
}
