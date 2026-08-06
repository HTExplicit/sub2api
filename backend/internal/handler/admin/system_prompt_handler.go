package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SystemPromptHandler exposes the independent business System Prompt catalog
// and singleton runtime policy to ordinary authenticated administrators.
type SystemPromptHandler struct {
	service *service.BusinessSystemPromptService
}

func NewSystemPromptHandler(promptService *service.BusinessSystemPromptService) *SystemPromptHandler {
	return &SystemPromptHandler{service: promptService}
}

type systemPromptRuntimeResponse struct {
	Enabled            bool      `json:"enabled"`
	ExposeServerPrompt bool      `json:"expose_server_prompt"`
	CompactEnabled     bool      `json:"compact_enabled"`
	TemplateID         int64     `json:"template_id"`
	VersionID          int64     `json:"version_id"`
	TemplateVersion    int64     `json:"template_version"`
	Revision           int64     `json:"revision"`
	SHA256             string    `json:"sha256"`
	ByteLength         int       `json:"byte_length"`
	Degraded           bool      `json:"degraded"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type systemPromptEnvelope struct {
	Templates []service.BusinessSystemPromptTemplate `json:"templates"`
	Runtime   systemPromptRuntimeResponse            `json:"runtime"`
}

type systemPromptCreateRequest struct {
	Slug             string `json:"slug" binding:"required"`
	Name             string `json:"name" binding:"required"`
	Description      string `json:"description"`
	Body             string `json:"body" binding:"required"`
	Note             string `json:"note"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type systemPromptUpdateRequest struct {
	Name             *string `json:"name"`
	Description      *string `json:"description"`
	ExpectedRevision int64   `json:"expected_revision"`
}

type systemPromptVersionRequest struct {
	Body                  string `json:"body" binding:"required"`
	Note                  string `json:"note"`
	ExpectedLatestVersion int64  `json:"expected_latest_version"`
	ExpectedRevision      int64  `json:"expected_revision"`
}

type systemPromptRuntimeRequest struct {
	ExpectedRevision   int64 `json:"expected_revision" binding:"required"`
	Enabled            bool  `json:"enabled"`
	ExposeServerPrompt bool  `json:"expose_server_prompt"`
	CompactEnabled     bool  `json:"compact_enabled"`
}

type systemPromptDuplicateRequest struct {
	Slug             string `json:"slug" binding:"required"`
	Name             string `json:"name" binding:"required"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type systemPromptPreviewRequest struct {
	TemplateID         int64           `json:"template_id"`
	VersionID          int64           `json:"version_id"`
	ClientInstructions string          `json:"client_instructions"`
	ServerInstructions string          `json:"server_instructions"`
	Protocol           string          `json:"protocol"`
	Compact            bool            `json:"compact"`
	Body               json.RawMessage `json:"body"`
}

func (h *SystemPromptHandler) actorID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return 0, false
	}
	return subject.UserID, true
}

func businessSystemPromptRuntimeResponse(snapshot service.BusinessSystemPromptSnapshot) systemPromptRuntimeResponse {
	return systemPromptRuntimeResponse{
		Enabled:            snapshot.Enabled,
		ExposeServerPrompt: snapshot.ExposeServerPrompt,
		CompactEnabled:     snapshot.CompactEnabled,
		TemplateID:         snapshot.TemplateID,
		VersionID:          snapshot.VersionID,
		TemplateVersion:    snapshot.TemplateVersion,
		Revision:           snapshot.Revision,
		SHA256:             strings.ToLower(snapshot.SHA256),
		ByteLength:         snapshot.ByteLength,
		Degraded:           snapshot.Degraded,
		UpdatedAt:          snapshot.UpdatedAt,
	}
}

func writeBusinessSystemPromptError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrBusinessSystemPromptRevisionConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "system_prompt_revision_conflict", "system_prompt_revision_conflict", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptUnavailable):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "system_prompt_unavailable", "system_prompt_unavailable", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptTemplateNotFound), errors.Is(err, service.ErrBusinessSystemPromptVersionNotFound):
		response.NotFound(c, "System prompt template or version not found")
	case errors.Is(err, service.ErrBusinessSystemPromptSeedProtected), errors.Is(err, service.ErrBusinessSystemPromptActive):
		response.ErrorWithDetails(c, http.StatusConflict, err.Error(), "system_prompt_delete_protected", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptInvalid):
		response.BadRequest(c, err.Error())
	default:
		response.ErrorFrom(c, err)
	}
}

func parsePositiveID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param(name)), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return id, true
}

// List returns catalog metadata and the current runtime state.
func (h *SystemPromptHandler) List(c *gin.Context) {
	items, err := h.service.ListTemplates(c.Request.Context())
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	snapshot, ok := h.service.CurrentSnapshot()
	if !ok {
		writeBusinessSystemPromptError(c, service.ErrBusinessSystemPromptUnavailable)
		return
	}
	response.Success(c, systemPromptEnvelope{Templates: items, Runtime: businessSystemPromptRuntimeResponse(snapshot)})
}

func (h *SystemPromptHandler) Runtime(c *gin.Context) {
	snapshot, ok := h.service.CurrentSnapshot()
	if !ok {
		writeBusinessSystemPromptError(c, service.ErrBusinessSystemPromptUnavailable)
		return
	}
	response.Success(c, businessSystemPromptRuntimeResponse(snapshot))
}

func (h *SystemPromptHandler) Get(c *gin.Context) {
	id, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	detail, err := h.service.GetTemplate(c.Request.Context(), id)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	snapshot, ok := h.service.CurrentSnapshot()
	if !ok {
		writeBusinessSystemPromptError(c, service.ErrBusinessSystemPromptUnavailable)
		return
	}
	response.Success(c, gin.H{"template": detail.Template, "versions": detail.Versions, "runtime": businessSystemPromptRuntimeResponse(snapshot)})
}

func (h *SystemPromptHandler) Versions(c *gin.Context) {
	id, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	detail, err := h.service.GetTemplate(c.Request.Context(), id)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, detail.Versions)
}

func (h *SystemPromptHandler) Create(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	var req systemPromptCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	created, err := h.service.CreateTemplate(c.Request.Context(), service.BusinessSystemPromptTemplateCreate{
		Slug: req.Slug, Name: req.Name, Description: req.Description, Body: req.Body, Note: req.Note,
	}, actorID, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	if len(created.Versions) > 0 {
		middleware.SetAuditExtra(c, map[string]any{
			"template_id": created.Template.ID, "template_version": created.Versions[0].Version,
			"new_sha256": created.Versions[0].SHA256, "byte_length": created.Versions[0].ByteLength,
			"revision": req.ExpectedRevision, "result": "created",
		})
	}
	response.Created(c, created)
}

func (h *SystemPromptHandler) Update(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	id, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	var req systemPromptUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.service.UpdateTemplate(c.Request.Context(), id, service.BusinessSystemPromptTemplateUpdate{Name: req.Name, Description: req.Description}, actorID, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"template_id": id, "revision": req.ExpectedRevision, "result": "updated"})
	response.Success(c, updated)
}

func (h *SystemPromptHandler) SaveVersion(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	templateID, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	var req systemPromptVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	version, err := h.service.CreateVersion(c.Request.Context(), templateID, req.Body, req.Note, actorID, req.ExpectedLatestVersion, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": templateID, "template_version": version.Version,
		"new_sha256": version.SHA256, "byte_length": version.ByteLength,
		"revision": req.ExpectedRevision, "result": "draft_saved",
	})
	response.Created(c, version)
}

func (h *SystemPromptHandler) Publish(c *gin.Context) {
	h.publish(c)
}

func (h *SystemPromptHandler) Rollback(c *gin.Context) {
	h.publish(c)
}

func (h *SystemPromptHandler) publish(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	templateID, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	versionID, ok := parsePositiveID(c, "version_id")
	if !ok {
		return
	}
	var req struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	if req.ExpectedRevision == 0 {
		req.ExpectedRevision, _ = strconv.ParseInt(strings.TrimSpace(c.Query("expected_revision")), 10, 64)
	}
	oldSnapshot, _ := h.service.CurrentSnapshot()
	snapshot, err := h.service.PublishVersion(c.Request.Context(), templateID, versionID, req.ExpectedRevision, actorID)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	result := "published"
	if strings.HasSuffix(c.FullPath(), "/rollback") {
		result = "rolled_back"
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": templateID, "template_version": snapshot.TemplateVersion,
		"old_sha256": oldSnapshot.SHA256, "new_sha256": snapshot.SHA256,
		"byte_length": snapshot.ByteLength, "revision": snapshot.Revision, "result": result,
	})
	response.Success(c, businessSystemPromptRuntimeResponse(snapshot))
}

func (h *SystemPromptHandler) Duplicate(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	sourceID, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	var req systemPromptDuplicateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	created, err := h.service.DuplicateTemplate(c.Request.Context(), sourceID, req.Slug, req.Name, actorID, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	extra := map[string]any{"template_id": created.Template.ID, "revision": req.ExpectedRevision, "result": "duplicated"}
	if len(created.Versions) > 0 {
		extra["template_version"] = created.Versions[0].Version
		extra["new_sha256"] = created.Versions[0].SHA256
		extra["byte_length"] = created.Versions[0].ByteLength
	}
	middleware.SetAuditExtra(c, extra)
	response.Created(c, created)
}

func (h *SystemPromptHandler) Delete(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	id, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	expectedRevision, _ := strconv.ParseInt(strings.TrimSpace(c.Query("expected_revision")), 10, 64)
	if err := h.service.DeleteTemplate(c.Request.Context(), id, actorID, expectedRevision); err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"template_id": id, "revision": expectedRevision, "result": "deleted"})
	response.Success(c, gin.H{"deleted": true})
}

func (h *SystemPromptHandler) UpdateRuntime(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	var req systemPromptRuntimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	snapshot, err := h.service.UpdateRuntime(c.Request.Context(), service.BusinessSystemPromptRuntimeUpdate{
		ExpectedRevision: req.ExpectedRevision, Enabled: req.Enabled,
		ExposeServerPrompt: req.ExposeServerPrompt, CompactEnabled: req.CompactEnabled, ActorID: actorID,
	})
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": snapshot.TemplateID, "template_version": snapshot.TemplateVersion,
		"new_sha256": snapshot.SHA256, "byte_length": snapshot.ByteLength,
		"revision": snapshot.Revision, "enabled": snapshot.Enabled,
		"expose_server_prompt": snapshot.ExposeServerPrompt, "compact_enabled": snapshot.CompactEnabled,
		"result": "runtime_updated",
	})
	response.Success(c, businessSystemPromptRuntimeResponse(snapshot))
}

func (h *SystemPromptHandler) PreviewMerge(c *gin.Context) {
	var req systemPromptPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	server := req.ServerInstructions
	var templateVersion int64
	if server == "" && req.TemplateID > 0 {
		detail, err := h.service.GetTemplate(c.Request.Context(), req.TemplateID)
		if err != nil {
			writeBusinessSystemPromptError(c, err)
			return
		}
		version, err := selectVersion(detail, req.VersionID)
		if err != nil {
			writeBusinessSystemPromptError(c, err)
			return
		}
		server = version.Body
		templateVersion = version.Version
	}
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(server)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": req.TemplateID, "template_version": templateVersion,
		"new_sha256": hash, "byte_length": byteLength, "result": "previewed",
	})
	response.Success(c, gin.H{"instructions": service.MergeBusinessSystemPromptInstructions(req.ClientInstructions, server)})
}

func (h *SystemPromptHandler) PreviewUpstream(c *gin.Context) {
	var req systemPromptPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.Body) == 0 || !json.Valid(req.Body) {
		response.BadRequest(c, "body must be valid JSON")
		return
	}
	server := req.ServerInstructions
	var versionID, templateVersion int64
	if req.TemplateID > 0 {
		detail, err := h.service.GetTemplate(c.Request.Context(), req.TemplateID)
		if err != nil {
			writeBusinessSystemPromptError(c, err)
			return
		}
		version, err := selectVersion(detail, req.VersionID)
		if err != nil {
			writeBusinessSystemPromptError(c, err)
			return
		}
		versionID = version.ID
		templateVersion = version.Version
		server = version.Body
	}
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(server)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": req.TemplateID, "template_version": templateVersion,
		"new_sha256": hash, "byte_length": byteLength, "result": "previewed",
	})
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		protocol = service.BusinessSystemPromptProtocolResponses
	}
	updated, application, err := service.ApplyBusinessSystemPromptToJSON(req.Body, service.BusinessSystemPromptSnapshot{
		Enabled: true, CompactEnabled: true, TemplateID: req.TemplateID, VersionID: versionID, TemplateVersion: templateVersion,
		Revision: 1, Body: server, SHA256: hash, ByteLength: byteLength,
	}, service.BusinessSystemPromptTarget{Platform: service.PlatformOpenAI, Protocol: protocol, Compact: req.Compact})
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	var decoded any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		response.BadRequest(c, "preview body could not be decoded")
		return
	}
	response.Success(c, gin.H{"body": decoded, "application": application})
}

func selectVersion(detail service.BusinessSystemPromptTemplateDetail, versionID int64) (service.BusinessSystemPromptVersion, error) {
	if len(detail.Versions) == 0 {
		return service.BusinessSystemPromptVersion{}, service.ErrBusinessSystemPromptVersionNotFound
	}
	if versionID == 0 {
		return detail.Versions[0], nil
	}
	for _, version := range detail.Versions {
		if version.ID == versionID {
			return version, nil
		}
	}
	return service.BusinessSystemPromptVersion{}, service.ErrBusinessSystemPromptVersionNotFound
}
