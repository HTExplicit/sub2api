package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type RemoteSkillPublicReader interface {
	LoadPublishedFile(context.Context, string) (service.RemoteSkillPublicFile, error)
}

type RemoteSkillHandler struct {
	reader RemoteSkillPublicReader
}

func NewRemoteSkillHandler(reader RemoteSkillPublicReader) *RemoteSkillHandler {
	return &RemoteSkillHandler{reader: reader}
}

func ProvideRemoteSkillHandler(registry *service.RemoteSkillRegistryService) *RemoteSkillHandler {
	return NewRemoteSkillHandler(registry)
}

func (h *RemoteSkillHandler) Serve(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	escapedPath := strings.ToLower(c.Request.URL.EscapedPath())
	if strings.Contains(escapedPath, "%2e") || strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
		c.Status(http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(c.Param("path"), "/")
	if h == nil || h.reader == nil || name == "" || strings.HasSuffix(name, "/") {
		c.Status(http.StatusNotFound)
		return
	}
	file, err := h.reader.LoadPublishedFile(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, service.ErrRemoteSkillPublicFileNotFound) || errors.Is(err, service.ErrBusinessSystemPromptBundleUnavailable) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("ETag", file.ETag)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", file.ContentType)
	c.Header("Content-Length", strconv.Itoa(len(file.Body)))
	if c.GetHeader("If-None-Match") == file.ETag {
		c.Status(http.StatusNotModified)
		return
	}
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}
	c.Data(http.StatusOK, file.ContentType, file.Body)
}
