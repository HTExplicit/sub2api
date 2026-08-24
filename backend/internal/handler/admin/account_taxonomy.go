package admin

import (
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type accountConsoleAdminService interface {
	ListAccountFolders(ctx context.Context) ([]service.AccountManagementFolder, error)
	CreateAccountFolder(ctx context.Context, input service.AccountTaxonomyInput) (*service.AccountManagementFolder, error)
	UpdateAccountFolder(ctx context.Context, id int64, input service.AccountTaxonomyInput) (*service.AccountManagementFolder, error)
	DeleteAccountFolder(ctx context.Context, id int64, moveAccounts bool) error
	ListAccountTags(ctx context.Context) ([]service.AccountManagementTag, error)
	CreateAccountTag(ctx context.Context, input service.AccountTaxonomyInput) (*service.AccountManagementTag, error)
	UpdateAccountTag(ctx context.Context, id int64, input service.AccountTaxonomyInput) (*service.AccountManagementTag, error)
	DeleteAccountTag(ctx context.Context, id int64) error
	SetAccountTaxonomy(ctx context.Context, accountID int64, assignment service.AccountTaxonomyAssignment) (*service.Account, error)
	ListAccountsConsole(ctx context.Context, page, pageSize int, filters service.AccountConsoleFilters) ([]service.Account, int64, error)
	GetAccountConsoleFacets(ctx context.Context, filters service.AccountConsoleFilters) (*service.AccountConsoleFacets, error)
}

type accountTaxonomyMutationAdminService interface {
	ReorderAccountFolders(ctx context.Context, orderedIDs []int64) ([]service.AccountManagementFolder, error)
	ReorderAccountTags(ctx context.Context, orderedIDs []int64) ([]service.AccountManagementTag, error)
	BulkUpdateAccountTaxonomy(ctx context.Context, input service.BulkAccountTaxonomyInput) (*service.BulkAccountTaxonomyResult, error)
}

func (h *AccountHandler) accountConsoleService() (accountConsoleAdminService, error) {
	console, ok := h.adminService.(accountConsoleAdminService)
	if !ok {
		return nil, infraerrors.New(501, "ACCOUNT_CONSOLE_UNAVAILABLE", "account console service is unavailable")
	}
	return console, nil
}

func (h *AccountHandler) accountTaxonomyMutationService() (accountTaxonomyMutationAdminService, error) {
	taxonomy, ok := h.adminService.(accountTaxonomyMutationAdminService)
	if !ok {
		return nil, infraerrors.New(501, "ACCOUNT_TAXONOMY_MUTATION_UNAVAILABLE", "account taxonomy mutation service is unavailable")
	}
	return taxonomy, nil
}

func parsePositivePathID(c *gin.Context, kind string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+kind+" ID")
		return 0, false
	}
	return id, true
}

func accountFolderDTO(folder service.AccountManagementFolder) dto.AccountManagementFolder {
	return dto.AccountManagementFolder{
		ID: folder.ID, Name: folder.Name, SortOrder: folder.SortOrder,
		AccountCount: folder.AccountCount, CreatedAt: folder.CreatedAt, UpdatedAt: folder.UpdatedAt,
	}
}

func accountTagDTO(tag service.AccountManagementTag) dto.AccountManagementTag {
	return dto.AccountManagementTag{
		ID: tag.ID, Name: tag.Name, SortOrder: tag.SortOrder,
		AccountCount: tag.AccountCount, CreatedAt: tag.CreatedAt, UpdatedAt: tag.UpdatedAt,
	}
}

func accountFacetDTO(option service.AccountFacetOption) dto.AccountFacetOption {
	return dto.AccountFacetOption{Value: option.Value, Label: option.Label, Count: option.Count}
}

func accountFacetsDTO(facets *service.AccountConsoleFacets) dto.AccountConsoleFacets {
	out := dto.AccountConsoleFacets{
		Total: facets.Total, UncategorizedCount: facets.UncategorizedCount,
		Platforms:         make([]dto.AccountFacetOption, 0, len(facets.Platforms)),
		Types:             make([]dto.AccountFacetOption, 0, len(facets.Types)),
		Statuses:          make([]dto.AccountFacetOption, 0, len(facets.Statuses)),
		Plans:             make([]dto.AccountFacetOption, 0, len(facets.Plans)),
		Proxies:           make([]dto.AccountFacetOption, 0, len(facets.Proxies)),
		Folders:           make([]dto.AccountManagementFolder, 0, len(facets.Folders)),
		Tags:              make([]dto.AccountManagementTag, 0, len(facets.Tags)),
		CindyTotal:        facets.CindyTotal,
		CindyInsufficient: facets.CindyInsufficient,
		CindyBanned:       facets.CindyBanned,
	}
	for _, item := range facets.Platforms {
		out.Platforms = append(out.Platforms, accountFacetDTO(item))
	}
	for _, item := range facets.Types {
		out.Types = append(out.Types, accountFacetDTO(item))
	}
	for _, item := range facets.Statuses {
		out.Statuses = append(out.Statuses, accountFacetDTO(item))
	}
	for _, item := range facets.Plans {
		out.Plans = append(out.Plans, accountFacetDTO(item))
	}
	for _, item := range facets.Proxies {
		out.Proxies = append(out.Proxies, accountFacetDTO(item))
	}
	for _, item := range facets.Folders {
		out.Folders = append(out.Folders, accountFolderDTO(item))
	}
	for _, item := range facets.Tags {
		out.Tags = append(out.Tags, accountTagDTO(item))
	}
	return out
}

type accountTaxonomyOrderRequest struct {
	OrderedIDs []int64 `json:"ordered_ids"`
}

type bulkAccountTaxonomyRequest struct {
	AccountIDs         []int64                   `json:"account_ids"`
	Filters            *BulkUpdateAccountFilters `json:"filters"`
	ExpectedMatchCount *int                      `json:"expected_match_count"`
	FolderAction       string                    `json:"folder_action"`
	FolderID           *int64                    `json:"folder_id"`
	TagAddIDs          []int64                   `json:"tag_add_ids"`
	TagRemoveIDs       []int64                   `json:"tag_remove_ids"`
}

func (h *AccountHandler) ListAccountFolders(c *gin.Context) {
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, err := console.ListAccountFolders(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.AccountManagementFolder, 0, len(items))
	for _, item := range items {
		out = append(out, accountFolderDTO(item))
	}
	response.Success(c, out)
}

func (h *AccountHandler) CreateAccountFolder(c *gin.Context) {
	var input service.AccountTaxonomyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := console.CreateAccountFolder(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accountFolderDTO(*item))
}

func (h *AccountHandler) UpdateAccountFolder(c *gin.Context) {
	id, ok := parsePositivePathID(c, "folder")
	if !ok {
		return
	}
	var input service.AccountTaxonomyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := console.UpdateAccountFolder(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accountFolderDTO(*item))
}

func (h *AccountHandler) DeleteAccountFolder(c *gin.Context) {
	id, ok := parsePositivePathID(c, "folder")
	if !ok {
		return
	}
	moveAccounts := parseBoolQueryWithDefault(c.Query("move_accounts"), false)
	console, err := h.accountConsoleService()
	if err == nil {
		err = console.DeleteAccountFolder(c.Request.Context(), id, moveAccounts)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true, "moved_to_uncategorized": moveAccounts})
}

func (h *AccountHandler) ReorderAccountFolders(c *gin.Context) {
	var req accountTaxonomyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountTaxonomyMutationService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, err := console.ReorderAccountFolders(c.Request.Context(), req.OrderedIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.AccountManagementFolder, 0, len(items))
	for _, item := range items {
		out = append(out, accountFolderDTO(item))
	}
	response.Success(c, out)
}

func (h *AccountHandler) ListAccountTags(c *gin.Context) {
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, err := console.ListAccountTags(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.AccountManagementTag, 0, len(items))
	for _, item := range items {
		out = append(out, accountTagDTO(item))
	}
	response.Success(c, out)
}

func (h *AccountHandler) CreateAccountTag(c *gin.Context) {
	var input service.AccountTaxonomyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := console.CreateAccountTag(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accountTagDTO(*item))
}

func (h *AccountHandler) UpdateAccountTag(c *gin.Context) {
	id, ok := parsePositivePathID(c, "tag")
	if !ok {
		return
	}
	var input service.AccountTaxonomyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := console.UpdateAccountTag(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accountTagDTO(*item))
}

func (h *AccountHandler) DeleteAccountTag(c *gin.Context) {
	id, ok := parsePositivePathID(c, "tag")
	if !ok {
		return
	}
	console, err := h.accountConsoleService()
	if err == nil {
		err = console.DeleteAccountTag(c.Request.Context(), id)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountHandler) ReorderAccountTags(c *gin.Context) {
	var req accountTaxonomyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountTaxonomyMutationService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, err := console.ReorderAccountTags(c.Request.Context(), req.OrderedIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.AccountManagementTag, 0, len(items))
	for _, item := range items {
		out = append(out, accountTagDTO(item))
	}
	response.Success(c, out)
}

func (h *AccountHandler) SetAccountTaxonomy(c *gin.Context) {
	accountID, ok := parsePositivePathID(c, "account")
	if !ok {
		return
	}
	var assignment service.AccountTaxonomyAssignment
	if err := c.ShouldBindJSON(&assignment); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	account, err := console.SetAccountTaxonomy(c.Request.Context(), accountID, assignment)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.accountResponseFromService(account))
}

func (h *AccountHandler) BulkUpdateAccountTaxonomy(c *gin.Context) {
	var req bulkAccountTaxonomyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.AccountIDs = normalizeInt64IDList(req.AccountIDs)
	seeds := accountJobSeeds(req.AccountIDs)
	if len(seeds) == 0 {
		seeds = ordinalAccountJobSeeds(1)
	}
	h.submitAccountJob(c, service.AccountJobKindBulkTaxonomy, req, seeds)
}

func splitQueryValues(c *gin.Context, keys ...string) []string {
	values := make([]string, 0)
	for _, key := range keys {
		for _, raw := range c.QueryArray(key) {
			for _, value := range strings.Split(raw, ",") {
				if value = strings.TrimSpace(value); value != "" {
					values = append(values, value)
				}
			}
		}
	}
	return values
}

func parseIDQueryValues(values []string, special string) ([]int64, bool, error) {
	ids := make([]int64, 0, len(values))
	includeSpecial := false
	for _, value := range values {
		if special != "" && value == special {
			includeSpecial = true
			continue
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, false, infraerrors.BadRequest("INVALID_ACCOUNT_FILTER", "invalid account filter value")
		}
		ids = append(ids, id)
	}
	return ids, includeSpecial, nil
}

func parseAccountConsoleFilters(c *gin.Context, groupID int64) (service.AccountConsoleFilters, error) {
	platforms := splitQueryValues(c, "platforms")
	if len(platforms) == 0 && strings.TrimSpace(c.Query("platform")) != "" {
		platforms = []string{strings.TrimSpace(c.Query("platform"))}
	}
	types := splitQueryValues(c, "types")
	if len(types) == 0 && strings.TrimSpace(c.Query("type")) != "" {
		types = []string{strings.TrimSpace(c.Query("type"))}
	}
	statuses := splitQueryValues(c, "statuses")
	if len(statuses) == 0 && strings.TrimSpace(c.Query("status")) != "" {
		statuses = []string{strings.TrimSpace(c.Query("status"))}
	}
	filters := service.AccountConsoleFilters{
		Platforms: platforms, Types: types, Statuses: statuses, Plans: splitQueryValues(c, "plans"),
		Search: strings.TrimSpace(c.Query("search")), GroupID: groupID,
		PrivacyMode: strings.TrimSpace(c.Query("privacy_mode")),
		SortBy:      c.DefaultQuery("sort_by", "name"), SortOrder: c.DefaultQuery("sort_order", "asc"),
		CindyOnly:          parseBoolQueryWithDefault(c.Query("cindy_only"), false),
		CindyBalanceStatus: strings.TrimSpace(c.Query("cindy_balance_status")),
		CindyHealthStatus:  strings.TrimSpace(c.Query("cindy_health_status")),
	}
	if filters.CindyBalanceStatus != "" && filters.CindyBalanceStatus != "insufficient" {
		return filters, infraerrors.BadRequest("INVALID_CINDY_BALANCE_STATUS", "invalid Cindy balance status")
	}
	if filters.CindyHealthStatus != "" && filters.CindyHealthStatus != "banned" {
		return filters, infraerrors.BadRequest("INVALID_CINDY_HEALTH_STATUS", "invalid Cindy health status")
	}
	var err error
	filters.FolderIDs, filters.IncludeUncategorized, err = parseIDQueryValues(splitQueryValues(c, "folders", "folder"), "uncategorized")
	if err != nil {
		return filters, err
	}
	filters.TagIDs, _, err = parseIDQueryValues(splitQueryValues(c, "tags"), "")
	if err != nil {
		return filters, err
	}
	filters.ProxyIDs, filters.IncludeDirect, err = parseIDQueryValues(splitQueryValues(c, "proxies"), "direct")
	if err != nil {
		return filters, err
	}
	filters.AccountIDs, _, err = parseIDQueryValues(splitQueryValues(c, "account_ids"), "")
	return filters, err
}

func hasAccountConsoleFilters(c *gin.Context) bool {
	for _, key := range []string{"platforms", "types", "statuses", "plans", "proxies", "folders", "folder", "tags", "account_ids", "cindy_only", "cindy_balance_status", "cindy_health_status"} {
		if _, ok := c.GetQuery(key); ok {
			return true
		}
	}
	return false
}

func (h *AccountHandler) GetAccountFacets(c *gin.Context) {
	var groupID int64
	if value := c.Query("group_id"); value == accountListGroupUngroupedQueryValue {
		groupID = service.AccountListGroupUngrouped
	} else if value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			response.ErrorFrom(c, infraerrors.BadRequest("INVALID_GROUP_FILTER", "invalid group filter"))
			return
		}
		groupID = parsed
	}
	filters, err := parseAccountConsoleFilters(c, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	console, err := h.accountConsoleService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	facets, err := console.GetAccountConsoleFacets(c.Request.Context(), filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accountFacetsDTO(facets))
}
