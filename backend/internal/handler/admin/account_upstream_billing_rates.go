package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// upstreamBillingRatesResponse is deliberately smaller than the regular
// account-list response. The frontend uses the returned ID order to detect a
// page boundary/order change and only applies snapshots when the page is
// otherwise stable.
type upstreamBillingRatesResponse struct {
	Items    []service.UpstreamBillingRateSnapshotItem `json:"items"`
	Total    int64                                     `json:"total"`
	Page     int                                       `json:"page"`
	PageSize int                                       `json:"page_size"`
}

// GetUpstreamBillingRates returns persisted upstream billing snapshots for the
// current account-list page. It never probes an upstream and never loads
// today-stats, usage, concurrency, or credentials for the response.
//
// It must resolve the same page as List for the same query: the account page
// applies the snapshots only when the total and the ID order equal the list's,
// and reloads the whole list otherwise.
//
// GET /api/v1/admin/accounts/upstream-billing-rates
func (h *AccountHandler) GetUpstreamBillingRates(c *gin.Context) {
	if h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "account service unavailable")
		return
	}

	page, pageSize := response.ParsePagination(c)
	platform := c.Query("platform")
	accountType := c.Query("type")
	status := c.Query("status")
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 100 {
		search = search[:100]
	}
	privacyMode := strings.TrimSpace(c.Query("privacy_mode"))
	sortBy := c.DefaultQuery("sort_by", "name")
	sortOrder := c.DefaultQuery("sort_order", "asc")

	// group_id is the name List reads; the older group applies only when
	// group_id is absent.
	groupQuery, hasGroupID := c.GetQuery("group_id")
	if !hasGroupID {
		groupQuery = c.Query("group")
	}
	var groupID int64
	if groupQuery != "" {
		if groupQuery == accountListGroupUngroupedQueryValue {
			groupID = service.AccountListGroupUngrouped
		} else {
			parsed, err := strconv.ParseInt(groupQuery, 10, 64)
			if err != nil || parsed < 0 {
				response.ErrorFrom(c, infraerrors.BadRequest("INVALID_GROUP_FILTER", "invalid group filter"))
				return
			}
			groupID = parsed
		}
	}

	var (
		accounts []service.Account
		total    int64
		err      error
	)
	if hasAccountConsoleFilters(c) {
		filters, filterErr := parseAccountConsoleFilters(c, groupID)
		if filterErr != nil {
			response.ErrorFrom(c, filterErr)
			return
		}
		var consoleService accountConsoleAdminService
		consoleService, err = h.accountConsoleService()
		if err == nil {
			accounts, total, err = consoleService.ListAccountsConsole(c.Request.Context(), page, pageSize, filters)
		}
	} else {
		accounts, total, err = h.adminService.ListAccounts(
			c.Request.Context(), page, pageSize, platform, accountType, status,
			search, groupID, privacyMode, sortBy, sortOrder,
		)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	payload := upstreamBillingRatesResponse{
		Items:    service.BuildUpstreamBillingRateSnapshotItems(accounts),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
	c.Header("Cache-Control", "private, no-cache")
	etag := buildUpstreamBillingRatesETag(payload)
	if etag != "" {
		c.Header("ETag", etag)
		if ifNoneMatchMatched(c.GetHeader("If-None-Match"), etag) {
			c.Status(http.StatusNotModified)
			return
		}
	}

	response.Success(c, payload)
}

func buildUpstreamBillingRatesETag(payload upstreamBillingRatesResponse) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "\"" + hex.EncodeToString(sum[:]) + "\""
}
