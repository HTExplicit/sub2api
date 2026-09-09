package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// This is a single ingress projection, not another forwarding/admission loop.
// The old count handler retains its existing billing eligibility and retry
// behavior, while managed account validation confines it to the retained path.
func prepareManagedModelLegacyMetadataHTTP(c *gin.Context, request *service.ManagedModelRequest) bool {
	if c.Request.Method != http.MethodPost || request.Endpoint != service.CompositeRouteEndpointCountTokens {
		rejectManagedModelHTTP(c)
		return false
	}
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil || !gjson.ValidBytes(body) || gjson.GetBytes(body, "model").String() != request.Route.PublicModel {
		rejectManagedModelHTTP(c)
		return false
	}
	body, err = sjson.SetBytes(body, "model", request.RoutingModel())
	if err != nil {
		rejectManagedModelHTTP(c)
		return false
	}
	ctx := service.WithManagedModelBranch(c.Request.Context(), *request.Branch)
	c.Request = c.Request.WithContext(ctx)
	requestmodel.ResetRequestBody(c.Request, body)
	return true
}

func managedModelMetadataPublicName(c *gin.Context, fallback string) string {
	if c != nil && c.Request != nil {
		if request, managed := service.ManagedModelRequestFromContext(c.Request.Context()); managed &&
			request.Endpoint == service.CompositeRouteEndpointCountTokens && request.Route.PublicModel != "" {
			return request.Route.PublicModel
		}
	}
	return fallback
}
