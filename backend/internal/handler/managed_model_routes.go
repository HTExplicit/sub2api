package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type managedModelRouteSource interface {
	LatestManagedModelGroup(context.Context, int64) (*service.Group, error)
	ValidateManagedModelCompilation(context.Context, *service.Group, *service.ManagedModelRequest) error
}

// ManagedModelRouteGuard checks public groups against the current repository
// before trusting an auth cache's managed flag. Otherwise the first publication
// could be bypassed by an older "unmanaged" API-key snapshot.
func (h *GatewayHandler) ManagedModelRouteGuard(maxNormalizedBytes ...int64) gin.HandlerFunc {
	var source managedModelRouteSource
	if h != nil && h.gatewayService != nil {
		source = h.gatewayService
	}
	var bodyLimit int64
	if len(maxNormalizedBytes) > 0 {
		bodyLimit = maxNormalizedBytes[0]
	}
	return managedModelRouteGuard(source, bodyLimit)
}

func managedModelRouteGuard(source managedModelRouteSource, bodyLimit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform == service.PlatformCindy ||
			(!apiKey.Group.ManagedModelRoutes.Enabled && apiKey.Group.IsExclusive) {
			c.Next()
			return
		}
		var latestGroup *service.Group
		if source != nil {
			latestGroup, _ = source.LatestManagedModelGroup(c.Request.Context(), apiKey.Group.ID)
		}
		if latestGroup == nil {
			rejectManagedModelHTTP(c)
			return
		}
		if !latestGroup.ManagedModelRoutes.Enabled {
			if apiKey.Group.ManagedModelRoutes.Enabled {
				rejectManagedModelHTTP(c)
				return
			}
			c.Next()
			return
		}
		groupCopy := *latestGroup
		groupCopy.ModelAllowlist = service.EffectiveManagedModelAllowlist(latestGroup)
		keyCopy := *apiKey
		keyCopy.Group = &groupCopy
		c.Set(string(middleware2.ContextKeyAPIKey), &keyCopy)
		// Scheduling and billing read the typed request context, not Gin's
		// API-key slot. Keep both views on this same current publication.
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, &groupCopy))
		if !middleware2.PrepareManagedModelRoute(c, &groupCopy, bodyLimit) {
			return
		}
		request, managed := service.ManagedModelRequestFromContext(c.Request.Context())
		if managed && source.ValidateManagedModelCompilation(c.Request.Context(), &groupCopy, request) != nil {
			rejectManagedModelHTTP(c)
			return
		}
		c.Next()
	}
}

func rejectManagedModelHTTP(c *gin.Context) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	middleware2.MarkIngressRejected(c, middleware2.IngressRejectModelNotAllowed)
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": service.ErrManagedModelRouteUnavailable.Error()}})
	c.Abort()
}
