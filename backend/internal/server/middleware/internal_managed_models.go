package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// rejectClientManagedModelSelectors reserves selectors at the authenticated
// gateway boundary even for private/unmanaged groups with no allowlist. It
// inspects the client body before channel/account mappings can hide ambiguity.
func rejectClientManagedModelSelectors(c *gin.Context, bodyLimit int64) bool {
	if c.Request == nil || isResponsesWebSocketRoute(c) {
		return true // WS frames have their own raw-client guard after upgrade.
	}
	if service.IsManagedModelSelector(groupModelAllowlistModelFromParams(c)) {
		return rejectManagedModelRoute(c)
	}
	if c.Request.URL != nil {
		for key, values := range c.Request.URL.Query() {
			if !strings.EqualFold(key, "model") {
				continue
			}
			for _, model := range values {
				if service.IsManagedModelSelector(model) {
					return rejectManagedModelRoute(c)
				}
			}
		}
	}
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		if _, read := groupModelAllowlistModelsFromBody(c, bodyLimit); !read {
			return false
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			return rejectManagedModelRoute(c)
		}
		if service.ContainsManagedModelSelector(c.GetHeader("Content-Type"), body) {
			return rejectManagedModelRoute(c)
		}
	}
	return true
}
