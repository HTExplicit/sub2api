package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *PluginHandler) Update(c *gin.Context) {
	id, ok := pluginIDParam(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.manager.MaxUploadBytes()+(1<<20))
	file, header, err := c.Request.FormFile("plugin")
	if err != nil {
		response.BadRequest(c, "请选择有效的 .s2plugin 文件")
		return
	}
	defer func() { _ = file.Close() }()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".s2plugin") {
		response.BadRequest(c, "插件包扩展名必须是 .s2plugin")
		return
	}
	revision, err := strconv.ParseInt(c.PostForm("expected_revision"), 10, 64)
	if err != nil || revision <= 0 {
		response.BadRequest(c, "需要当前插件版本修订号")
		return
	}
	var actor *int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		actor = &subject.UserID
	}
	plugin, err := h.manager.Update(c.Request.Context(), id, revision, c.PostForm("expected_package_sha256"), file, actor)
	if err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}
	response.Success(c, plugin)
}

func (h *PluginHandler) FollowBundledVersion(c *gin.Context) {
	id, ok := pluginIDParam(c)
	if !ok {
		return
	}
	var input struct {
		Revision int64  `json:"expected_revision"`
		Digest   string `json:"expected_package_sha256"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Revision <= 0 || len(input.Digest) != 64 {
		response.BadRequest(c, "需要当前插件版本与摘要")
		return
	}
	plugin, err := h.manager.FollowBundledVersion(c.Request.Context(), id, input.Revision, input.Digest)
	if err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}
	response.Success(c, plugin)
}
