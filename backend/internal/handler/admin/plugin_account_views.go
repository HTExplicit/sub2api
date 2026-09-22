package admin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
)

const accountViewEnvelopeLimit = 4 * 1024 * 1024

func decodeAccountViewEnvelope(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return service.ErrAccountViewInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return service.ErrAccountViewInvalid
	}
	return nil
}

// AccountViewRequest is request-local and is a no-op for native core requests.
// It never changes X-Sub2API-Plugin: that is the independent action owner.
func (h *PluginHandler) AccountViewRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		encoded := c.GetHeader(extensionv1.AccountViewHeader)
		binding := c.GetHeader("X-Sub2API-Plugin-UI-Session")
		var header *extensionv1.AccountViewIdentityV1
		if encoded != "" {
			if len(encoded) > 2048 {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			raw, err := base64.RawURLEncoding.DecodeString(encoded)
			var identity extensionv1.AccountViewIdentityV1
			if err != nil || decodeAccountViewEnvelope(raw, &identity) != nil {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			header = &identity
		}
		var request *extensionv1.AccountViewContextV1
		var envelope struct {
			ViewContext       json.RawMessage `json:"view_context"`
			AccountID         int64           `json:"account_id"`
			AccountIDs        []int64         `json:"account_ids"`
			SurvivorAccountID int64           `json:"survivor_account_id"`
			LoserAccountIDs   []int64         `json:"loser_account_ids"`
			ReviewJobID       int64           `json:"review_job_id"`
			Items             []struct {
				AccountID int64 `json:"account_id"`
			} `json:"items"`
		}
		// Large native import/create bodies retain the original path and limits.
		// View-sourced existing-target requests are explicitly bounded JSON only.
		if c.Request.Body != nil && c.Request.ContentLength != 0 && strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") && (header != nil || binding != "" || (c.Request.ContentLength > 0 && c.Request.ContentLength <= accountViewEnvelopeLimit)) {
			raw, err := io.ReadAll(io.LimitReader(c.Request.Body, accountViewEnvelopeLimit+1))
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
			if err != nil || len(raw) > accountViewEnvelopeLimit {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			// Native bodies may be arrays or have unrelated heterogeneous items.
			// Only reserved origin evidence opts into this typed envelope; leave
			// malformed/native body validation and response contracts to its handler.
			if header == nil && binding == "" {
				var reserved map[string]json.RawMessage
				if json.Unmarshal(raw, &reserved) != nil || reserved == nil {
					c.Next()
					return
				}
				viewRaw := bytes.TrimSpace(reserved["view_context"])
				inheritsReview := c.Request.Method == http.MethodPost && c.FullPath() == "/api/v1/admin/accounts/duplicates/merge" && len(reserved["review_job_id"]) > 0 && string(reserved["review_job_id"]) != "0" && string(reserved["review_job_id"]) != "null"
				if (len(viewRaw) == 0 || string(viewRaw) == "null") && !inheritsReview {
					c.Next()
					return
				}
			}
			if json.Unmarshal(raw, &envelope) != nil {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			if len(envelope.ViewContext) > 0 && string(envelope.ViewContext) != "null" {
				var captured extensionv1.AccountViewContextV1
				if decodeAccountViewEnvelope(envelope.ViewContext, &captured) != nil {
					accountViewRequestError(c, service.ErrAccountViewInvalid)
					return
				}
				request = &captured
			} else if header != nil && string(envelope.ViewContext) == "null" {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
		}
		if header != nil && strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/") {
			accountViewRequestError(c, service.ErrAccountViewInvalid)
			return
		}
		if header != nil && c.Request.Body != nil && c.Request.ContentLength != 0 && !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			accountViewRequestError(c, service.ErrAccountViewInvalid)
			return
		}
		if header != nil && request != nil && *header != request.AccountViewIdentityV1 {
			accountViewRequestError(c, service.ErrAccountViewInvalid)
			return
		}
		if envelope.ReviewJobID > 0 && c.Request.Method == http.MethodPost && c.FullPath() == "/api/v1/admin/accounts/duplicates/merge" {
			actor, ok := middleware.GetAuthSubjectFromContext(c)
			if !ok || h == nil || h.jobs == nil {
				accountViewRequestError(c, service.ErrAccountViewUnavailable)
				return
			}
			captured, reviewIDs, err := h.jobs.AccountViewFromReviewJob(c.Request.Context(), envelope.ReviewJobID, actor.UserID)
			if err != nil {
				accountViewRequestError(c, err)
				return
			}
			if (captured == nil && (request != nil || header != nil)) || (captured != nil && header != nil && *header != captured.AccountViewIdentityV1) {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			if request != nil && captured != nil {
				query, err := service.NormalizeAccountViewQuery(request.Query)
				if err != nil {
					accountViewRequestError(c, err)
					return
				}
				request.Query = query
				left, _ := json.Marshal(request)
				right, _ := json.Marshal(captured)
				if !bytes.Equal(left, right) {
					accountViewRequestError(c, service.ErrAccountViewInvalid)
					return
				}
			}
			selected := append([]int64{envelope.SurvivorAccountID}, envelope.LoserAccountIDs...)
			slices.Sort(selected)
			slices.Sort(reviewIDs)
			if !slices.Equal(selected, reviewIDs) {
				accountViewRequestError(c, service.ErrAccountViewScope)
				return
			}
			request = captured
		}
		if request == nil && header != nil {
			request = &extensionv1.AccountViewContextV1{AccountViewIdentityV1: *header}
		}
		if binding != "" {
			primary, _ := strconv.ParseInt(c.GetHeader("X-Sub2API-Plugin"), 10, 64)
			if strings.HasPrefix(c.FullPath(), "/api/v1/admin/plugins/:id") {
				pathID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
				if primary != 0 && primary != pathID {
					accountViewRequestError(c, service.ErrAccountViewInvalid)
					return
				}
				primary = pathID
			}
			actor, ok := middleware.GetAuthSubjectFromContext(c)
			if !ok || h == nil || h.manager == nil || h.manager.ValidateUIRequestBinding(c.Request.Context(), binding, primary, actor.UserID, header) != nil {
				accountViewRequestError(c, service.ErrAccountViewUnavailable)
				return
			}
		}
		if request == nil {
			c.Next()
			return
		}
		// Generic plugin RPC can call back into Host.Call without an origin-view
		// proof. View UI uses named resources/jobs, whose host handlers retain it.
		if c.Request.Method == http.MethodPost && (c.FullPath() == "/api/v1/admin/plugins/:id/actions" || c.FullPath() == "/api/v1/admin/plugins/:id/jobs") {
			accountViewRequestError(c, service.ErrAccountViewUnsupportedOperation)
			return
		}
		if h == nil || h.manager == nil {
			accountViewRequestError(c, service.ErrAccountViewUnavailable)
			return
		}
		retainedRequest := c.Request.Method == http.MethodGet && c.FullPath() == "/api/v1/admin/plugins/:id/resources"
		if (c.Request.Method == http.MethodGet && (c.FullPath() == "/api/v1/admin/account-jobs/:id" || c.FullPath() == "/api/v1/admin/account-jobs/:id/items" || c.FullPath() == "/api/v1/admin/account-jobs/:id/result-account-ids")) || (c.Request.Method == http.MethodPost && c.FullPath() == "/api/v1/admin/account-jobs/:id/cancel") {
			jobID, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || jobID <= 0 || h.jobs == nil {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			job, err := h.jobs.Get(c.Request.Context(), jobID)
			if err != nil {
				accountViewRequestError(c, err)
				return
			}
			if binding != "" || c.GetHeader("X-Sub2API-Plugin") != "" {
				primaryID, parseErr := strconv.ParseInt(c.GetHeader("X-Sub2API-Plugin"), 10, 64)
				owner, ownerErr := service.AccountJobPluginExecution(job.Metadata)
				if parseErr != nil || primaryID <= 0 || ownerErr != nil || owner.ID != primaryID {
					accountViewRequestError(c, service.ErrAccountViewScope)
					return
				}
			}
			stored, err := service.AccountJobViewExecution(job.Metadata)
			if err != nil || (stored != nil && stored.AccountViewIdentityV1 != request.AccountViewIdentityV1) {
				accountViewRequestError(c, service.ErrAccountViewInvalid)
				return
			}
			retainedRequest = true
		}
		for _, descriptor := range h.resources {
			if descriptor.Retained && descriptor.Method == c.Request.Method && descriptor.Path == c.FullPath() {
				retainedRequest = true
			}
		}
		if retainedRequest {
			ctx, err := h.manager.BindRetainedAccountView(c.Request.Context(), *request)
			if err != nil {
				accountViewRequestError(c, err)
				return
			}
			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return
		}
		listRequest := c.Request.Method == http.MethodGet && (c.FullPath() == "/api/v1/admin/accounts" || c.FullPath() == "/api/v1/admin/accounts/facets" || c.FullPath() == "/api/v1/admin/accounts/data")
		if listRequest {
			query, err := accountViewHTTPQuery(c)
			if err != nil {
				accountViewRequestError(c, err)
				return
			}
			request.Query = query
		}
		ctx, release, err := h.manager.BindAccountViewRequest(c.Request.Context(), *request)
		if err != nil {
			accountViewRequestError(c, err)
			return
		}
		defer release()
		c.Request = c.Request.WithContext(ctx)
		reserved := map[string]string{}
		for _, key := range []string{"cindy_only", "cindy_balance_status", "cindy_health_status"} {
			if value, present := c.GetQuery(key); present {
				reserved[key] = value
			}
		}
		if err := service.ValidateAccountViewPredicateQuery(ctx, reserved); err != nil {
			accountViewRequestError(c, err)
			return
		}
		var ids []int64
		accountIDPath := false
		for _, prefix := range []string{"/api/v1/admin/accounts/:id", "/api/v1/admin/openai/accounts/:id", "/api/v1/admin/grok/accounts/:id", "/api/v1/admin/cn-providers/accounts/:id"} {
			accountIDPath = accountIDPath || strings.HasPrefix(c.FullPath(), prefix)
		}
		if accountIDPath {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				accountViewRequestError(c, service.ErrAccountViewScope)
				return
			}
			ids = append(ids, id)
		}
		if envelope.AccountID != 0 {
			ids = append(ids, envelope.AccountID)
		}
		if envelope.SurvivorAccountID != 0 {
			ids = append(ids, envelope.SurvivorAccountID)
		}
		ids = append(ids, envelope.LoserAccountIDs...)
		ids = append(ids, envelope.AccountIDs...)
		for _, item := range envelope.Items {
			ids = append(ids, item.AccountID)
		}
		if listRequest {
			ids = append(ids, request.Query.AccountIDs...)
		}
		if len(ids) > 3200 {
			accountViewRequestError(c, service.ErrAccountViewInvalid)
			return
		}
		if len(ids) > 0 {
			if err := service.ValidateAccountViewTargets(ctx, ids); err != nil {
				accountViewRequestError(c, err)
				return
			}
		}
		c.Next()
	}
}

func accountViewRequestError(c *gin.Context, err error) { response.ErrorFrom(c, err); c.Abort() }

func accountViewHTTPQuery(c *gin.Context) (extensionv1.AccountViewQueryV1, error) {
	allowed := map[string]bool{}
	for _, key := range []string{"platform", "platforms", "type", "types", "status", "statuses", "plans", "proxies", "folder", "folders", "tags", "account_ids", "ids", "group_id", "privacy_mode", "search", "sort_by", "sort_order", "page", "page_size", "lite", "include_scheduler_score", "cindy_only", "cindy_balance_status", "cindy_health_status"} {
		allowed[key] = true
	}
	for key := range c.Request.URL.Query() {
		if !allowed[key] {
			return extensionv1.AccountViewQueryV1{}, service.ErrAccountViewInvalid
		}
	}
	var groupID int64
	if group := c.Query("group_id"); group == "ungrouped" {
		groupID = service.AccountListGroupUngrouped
	} else if group != "" {
		id, err := strconv.ParseInt(group, 10, 64)
		if err != nil || id < 0 {
			return extensionv1.AccountViewQueryV1{}, service.ErrAccountViewInvalid
		}
		groupID = id
	}
	filters, err := parseAccountConsoleFilters(c, groupID)
	if err != nil {
		return extensionv1.AccountViewQueryV1{}, err
	}
	query := extensionv1.AccountViewQueryV1{Platforms: filters.Platforms, Types: filters.Types, Statuses: filters.Statuses, Plans: filters.Plans, Tags: filters.TagIDs, AccountIDs: filters.AccountIDs, GroupID: groupID, PrivacyMode: filters.PrivacyMode, Search: filters.Search, SortBy: filters.SortBy, SortOrder: filters.SortOrder}
	for _, id := range filters.ProxyIDs {
		query.Proxies = append(query.Proxies, strconv.FormatInt(id, 10))
	}
	if filters.IncludeDirect {
		query.Proxies = append(query.Proxies, "direct")
	}
	for _, id := range filters.FolderIDs {
		query.Folders = append(query.Folders, strconv.FormatInt(id, 10))
	}
	if filters.IncludeUncategorized {
		query.Folders = append(query.Folders, "uncategorized")
	}
	return service.NormalizeAccountViewQuery(query)
}
