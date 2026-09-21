package admin

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	pluginRevisionHeader = "X-Sub2API-Plugin-Revision"
	pluginPackageHeader  = "X-Sub2API-Plugin-Package"
)

var pluginPackageDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func bindPluginHTTPPrecondition(c *gin.Context, revisionRequired, packageRequired bool) bool {
	revision, digest := strings.TrimSpace(c.GetHeader(pluginRevisionHeader)), strings.TrimSpace(c.GetHeader(pluginPackageHeader))
	if (revisionRequired && revision == "") || (packageRequired && digest == "") {
		response.ErrorWithDetails(c, http.StatusPreconditionRequired, "Reload the plugin view before submitting this operation", "PLUGIN_PRECONDITION_REQUIRED", nil)
		return false
	}
	ctx := c.Request.Context()
	if digest != "" {
		if len(c.Request.Header.Values(pluginPackageHeader)) != 1 || !pluginPackageDigestPattern.MatchString(digest) {
			response.ErrorWithDetails(c, http.StatusBadRequest, "Invalid plugin package precondition", "PLUGIN_PRECONDITION_INVALID", nil)
			return false
		}
		ctx = service.WithPluginExpectedPackage(ctx, digest)
	}
	if revision != "" {
		value, err := strconv.ParseInt(revision, 10, 64)
		if len(c.Request.Header.Values(pluginRevisionHeader)) != 1 || err != nil || value <= 0 || strconv.FormatInt(value, 10) != revision {
			response.ErrorWithDetails(c, http.StatusBadRequest, "Invalid plugin revision precondition", "PLUGIN_PRECONDITION_INVALID", nil)
			return false
		}
		ctx = service.WithPluginExpectedRevision(ctx, value)
	}
	c.Request = c.Request.WithContext(ctx)
	return true
}

func pluginPreconditionError(c *gin.Context, err error) bool {
	if errors.Is(err, service.ErrPluginStateChanged) || errors.Is(err, service.ErrPluginUISessionChanged) {
		response.ErrorWithDetails(c, http.StatusConflict, "Plugin changed; reload explicitly before retrying", "PLUGIN_STATE_CHANGED", nil)
		return true
	}
	return false
}

func writePluginConfigSnapshot(c *gin.Context, snapshot *service.PluginConfigSnapshot) {
	c.Header("Cache-Control", "private, no-store")
	c.Header(pluginRevisionHeader, strconv.FormatInt(snapshot.Revision, 10))
	c.Header(pluginPackageHeader, snapshot.PackageSHA256)
	c.Data(http.StatusOK, "application/json; charset=utf-8", snapshot.Config)
}
