package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func managedModelEndpoint(path string) string {
	switch {
	case strings.HasSuffix(path, "/responses/compact"):
		return service.CompositeRouteEndpointResponses
	case strings.HasSuffix(path, "/messages/count_tokens"):
		return service.CompositeRouteEndpointCountTokens
	case strings.HasSuffix(path, "/messages"):
		return service.CompositeRouteEndpointMessages
	case strings.HasSuffix(path, "/chat/completions"):
		return service.CompositeRouteEndpointChatCompletions
	case strings.HasSuffix(path, "/responses"):
		return service.CompositeRouteEndpointResponses
	default:
		return ""
	}
}

// PrepareManagedModelRoute runs before the ordinary allowlist so declared
// aliases are resolved to their public ID before that list is evaluated.
func PrepareManagedModelRoute(c *gin.Context, group *service.Group, bodyLimit int64) bool {
	if group == nil || !group.ManagedModelRoutes.Enabled || c.Request == nil {
		return true
	}
	if request, prepared := service.ManagedModelRequestFromContext(c.Request.Context()); prepared && request.GroupID == group.ID {
		return true
	}
	if isResponsesWebSocketRoute(c) {
		return true // The first frame and every later turn use the same guard.
	}
	path := c.Request.URL.Path
	if c.Request.Method == http.MethodGet && (strings.HasSuffix(path, "/models") || strings.HasSuffix(path, "/models/capabilities") || strings.HasSuffix(path, "/usage")) {
		return true
	}
	endpoint := managedModelEndpoint(path)
	if c.Request.Method != http.MethodPost || endpoint == "" {
		return rejectManagedModelRoute(c)
	}
	if _, read := groupModelAllowlistModelsFromBody(c, bodyLimit); !read {
		return false
	}
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		return rejectManagedModelRoute(c)
	}
	body, request, err := service.PrepareManagedModelRequest(group, endpoint, body, "")
	if err != nil || request == nil {
		return rejectManagedModelRoute(c)
	}
	c.Request = c.Request.WithContext(service.WithManagedModelRequest(c.Request.Context(), request))
	requestmodel.ResetRequestBody(c.Request, body)
	return true
}

func rejectManagedModelRoute(c *gin.Context) bool {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	MarkIngressRejected(c, IngressRejectModelNotAllowed)
	groupModelAllowlistErrorWriter(c)(c, http.StatusNotFound, service.ErrManagedModelRouteUnavailable.Error())
	c.Abort()
	return false
}
