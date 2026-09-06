package web

import (
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterFrontendFallback must run after API route registration. Matched
// routes remain owned by Gin; only unmatched document requests reach the SPA.
// Reserved namespaces come from the actual route table, not a second list of
// API aliases that can drift when new gateway endpoints are added.
func RegisterFrontendFallback(router *gin.Engine, frontend gin.HandlerFunc) {
	namespaces := make(map[string]struct{})
	for _, route := range router.Routes() {
		if namespace := frontendRouteNamespace(route.Path); namespace != "" {
			namespaces[namespace] = struct{}{}
		}
	}
	router.HandleMethodNotAllowed = true
	router.NoMethod(func(c *gin.Context) {
		frontendRouteError(c, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed for this API endpoint")
	})
	router.NoRoute(func(c *gin.Context) {
		_, apiPath := namespaces[frontendRouteNamespace(c.Request.URL.Path)]
		if frontend == nil || apiPath || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			frontendRouteError(c, http.StatusNotFound, "not_found", "Endpoint not found")
			return
		}
		frontend(c)
	})
}

func frontendRouteNamespace(value string) string {
	cleaned := strings.Trim(path.Clean("/"+strings.TrimSpace(value)), "/")
	namespace, _, _ := strings.Cut(cleaned, "/")
	return namespace
}

func frontendRouteError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": code, "message": message,
	}})
}
