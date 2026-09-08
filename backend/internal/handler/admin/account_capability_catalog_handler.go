package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountCapabilityCatalogHandler struct {
	catalog *service.AccountCapabilityCatalogService
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
	result, err := h.catalog.Candidates(c.Request.Context(), service.AccountCapabilityCandidateFilter{
		FolderIDs: folderIDs, AccountIDs: accountIDs, Search: c.Query("search"), Status: c.Query("status"),
		Page: positiveAccountJobQuery(c.Query("page"), 1), PageSize: positiveAccountJobQuery(c.Query("page_size"), 50),
	})
	if err != nil {
		response.InternalError(c, "Unable to read capability candidates")
		return
	}
	response.Success(c, result)
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
