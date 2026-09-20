package admin

import (
	"errors"
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
		ctx, release, err := h.manager.BindResourceContext(c.Request.Context(), id, c.GetHeader("X-Sub2API-Plugin-Package"), descriptor.ResourceGrant)
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
		c.Next()
	}
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
