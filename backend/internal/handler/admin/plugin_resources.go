package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
)

// RegisterResource is called only during route assembly. The guard runs on the
// existing route, so its authentication, audit, compliance and step-up chain is
// identical for host UI, plugin UI and compatibility API callers.
func (h *PluginHandler) RegisterResource(descriptor extensionv1.ResourceDescriptor) gin.HandlerFunc {
	if h == nil {
		return func(c *gin.Context) {
			response.Error(c, http.StatusServiceUnavailable, "Plugin resources are unavailable")
			c.Abort()
		}
	}
	if h.resources == nil {
		h.resources = map[string]extensionv1.ResourceDescriptor{}
	}
	if _, exists := h.resources[descriptor.Name]; exists {
		panic("duplicate plugin resource registration")
	}
	h.resources[descriptor.Name] = descriptor
	return func(c *gin.Context) {
		id := int64(0)
		if raw := c.GetHeader("X-Sub2API-Plugin"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed <= 0 {
				response.BadRequest(c, "Invalid plugin context")
				c.Abort()
				return
			}
			id = parsed
		}
		if descriptor.Retained && id == 0 {
			c.Next()
			return
		}
		ctx, release, err := h.manager.BindResourceContext(c.Request.Context(), id, c.GetHeader("X-Sub2API-Plugin-Package"), descriptor.ResourceGrant, descriptor.Retained)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, service.ErrExtensionOperationDisabled) {
				status = http.StatusNotFound
			}
			if errors.Is(err, service.ErrPluginUISessionChanged) {
				status = http.StatusConflict
			}
			response.Error(c, status, err.Error())
			c.Abort()
			return
		}
		defer release()
		c.Request = c.Request.WithContext(ctx)
		ids, filtered, err := resourceAccountTargets(c, descriptor)
		if err != nil {
			response.BadRequest(c, err.Error())
			c.Abort()
			return
		}
		if len(ids) > 0 || filtered {
			if err = h.manager.ValidateResourceAccounts(ctx, descriptor.Capability, ids, filtered, descriptor.FilterPlatform, descriptor.FilterAccountType); err != nil {
				response.Error(c, http.StatusForbidden, err.Error())
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func resourceAccountTargets(c *gin.Context, descriptor extensionv1.ResourceDescriptor) ([]int64, bool, error) {
	var ids []int64
	if descriptor.AccountParam != "" {
		id, err := strconv.ParseInt(c.Param(descriptor.AccountParam), 10, 64)
		if err != nil || id <= 0 {
			return nil, false, errors.New("invalid resource account")
		}
		ids = append(ids, id)
	}
	if descriptor.AccountBodyField == "" && descriptor.AccountItemsField == "" && descriptor.FilterField == "" && descriptor.AccountScopeField == "" {
		return ids, false, nil
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return nil, false, errors.New("resource account selection exceeds limit")
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return nil, false, errors.New("invalid resource selection")
	}
	if descriptor.AccountScopeField != "" {
		var scope struct {
			Mode       string  `json:"mode"`
			AccountIDs []int64 `json:"account_ids"`
			Filters    struct {
				AccountIDs []int64 `json:"account_ids"`
			} `json:"filters"`
		}
		if json.Unmarshal(body[descriptor.AccountScopeField], &scope) != nil {
			return nil, false, errors.New("invalid resource account scope")
		}
		if scope.Mode != "selected" {
			return ids, true, nil
		}
		selected := scope.AccountIDs
		if len(selected) == 0 {
			selected = scope.Filters.AccountIDs
		}
		if len(selected) == 0 || len(selected) > 3200 {
			return nil, false, errors.New("invalid resource account list")
		}
		return append(ids, selected...), false, nil
	}
	if value := body[descriptor.AccountBodyField]; descriptor.AccountBodyField != "" && len(value) > 0 {
		var direct []int64
		if json.Unmarshal(value, &direct) != nil {
			return nil, false, errors.New("invalid resource account list")
		}
		ids = append(ids, direct...)
	}
	if value := body[descriptor.AccountItemsField]; descriptor.AccountItemsField != "" && len(value) > 0 {
		var items []struct {
			AccountID int64 `json:"account_id"`
		}
		if json.Unmarshal(value, &items) != nil {
			return nil, false, errors.New("invalid resource account items")
		}
		for _, item := range items {
			ids = append(ids, item.AccountID)
		}
	}
	if len(ids) > 3200 {
		return nil, false, errors.New("too many resource accounts")
	}
	filter := body[descriptor.FilterField]
	return ids, descriptor.FilterField != "" && len(filter) > 0 && string(filter) != "null", nil
}

func (h *PluginHandler) Resources(c *gin.Context)     { h.listResources(c, "admin") }
func (h *PluginHandler) UserResources(c *gin.Context) { h.listResources(c, "user") }
func (h *PluginHandler) listResources(c *gin.Context, permission string) {
	id, ok := pluginIDParam(c)
	if !ok {
		return
	}
	registered := make([]extensionv1.ResourceDescriptor, 0, len(h.resources))
	for _, descriptor := range h.resources {
		registered = append(registered, descriptor)
	}
	sort.Slice(registered, func(i, j int) bool { return registered[i].Name < registered[j].Name })
	descriptors, err := h.manager.ResourceDescriptors(c.Request.Context(), id, permission, registered)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, descriptors)
}
