package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SystemPromptHandler exposes the independent business System Prompt catalog
// and singleton runtime policy to ordinary authenticated administrators.
type SystemPromptHandler struct {
	service       *service.BusinessSystemPromptService
	skillRegistry *service.RemoteSkillRegistryService
}

func NewSystemPromptHandler(promptService *service.BusinessSystemPromptService, skillRegistry *service.RemoteSkillRegistryService) *SystemPromptHandler {
	return &SystemPromptHandler{service: promptService, skillRegistry: skillRegistry}
}

type systemPromptRuntimeResponse struct {
	Enabled                       bool      `json:"enabled"`
	ExposeServerPrompt            bool      `json:"expose_server_prompt"`
	CompactEnabled                bool      `json:"compact_enabled"`
	TemplateID                    int64     `json:"template_id"`
	VersionID                     int64     `json:"version_id"`
	TemplateVersion               int64     `json:"template_version"`
	Revision                      int64     `json:"revision"`
	SHA256                        string    `json:"sha256"`
	ByteLength                    int       `json:"byte_length"`
	CompositionMode               string    `json:"composition_mode"`
	BundleID                      string    `json:"bundle_id,omitempty"`
	BundleManifestSHA256          string    `json:"bundle_manifest_sha256,omitempty"`
	RegistryRevision              int64     `json:"registry_revision,omitempty"`
	RegistryRawTreeSHA256         string    `json:"registry_raw_tree_sha256,omitempty"`
	RegistryEffectiveTreeSHA256   string    `json:"registry_effective_tree_sha256,omitempty"`
	RegistryPromptRawSHA256       string    `json:"registry_prompt_raw_sha256,omitempty"`
	RegistryPromptEffectiveSHA256 string    `json:"registry_prompt_effective_sha256,omitempty"`
	RegistryUpstreamSourceID      string    `json:"registry_upstream_source_id,omitempty"`
	RegistryUpstreamRoot          string    `json:"registry_upstream_root,omitempty"`
	RegistryPublicRoot            string    `json:"registry_public_root,omitempty"`
	BundleAvailable               bool      `json:"bundle_available"`
	BundleDegraded                bool      `json:"bundle_degraded"`
	DegradedReason                string    `json:"degraded_reason,omitempty"`
	Degraded                      bool      `json:"degraded"`
	UpdatedAt                     time.Time `json:"updated_at"`
}

type systemPromptEnvelope struct {
	Templates []service.BusinessSystemPromptTemplate `json:"templates"`
	Runtime   systemPromptRuntimeResponse            `json:"runtime"`
}

type systemPromptCreateRequest struct {
	Slug                 string `json:"slug" binding:"required"`
	Name                 string `json:"name" binding:"required"`
	Description          string `json:"description"`
	Body                 string `json:"body" binding:"required"`
	Note                 string `json:"note"`
	CompositionMode      string `json:"composition_mode"`
	BundleID             string `json:"bundle_id"`
	BundleManifestSHA256 string `json:"bundle_manifest_sha256"`
	ExpectedRevision     int64  `json:"expected_revision"`
}

type systemPromptUpdateRequest struct {
	Name             *string `json:"name"`
	Description      *string `json:"description"`
	ExpectedRevision int64   `json:"expected_revision"`
}

type systemPromptVersionRequest struct {
	Body                  string `json:"body" binding:"required"`
	Note                  string `json:"note"`
	CompositionMode       string `json:"composition_mode"`
	BundleID              string `json:"bundle_id"`
	BundleManifestSHA256  string `json:"bundle_manifest_sha256"`
	ExpectedLatestVersion int64  `json:"expected_latest_version"`
	ExpectedRevision      int64  `json:"expected_revision"`
}

type systemPromptSourceSyncRequest struct {
	ExpectedLatestVersion int64 `json:"expected_latest_version" binding:"required"`
	ExpectedRevision      int64 `json:"expected_revision" binding:"required"`
}

type systemPromptSourceSyncVersionResponse struct {
	ID                   int64  `json:"id"`
	TemplateID           int64  `json:"template_id"`
	Version              int64  `json:"version"`
	SHA256               string `json:"sha256"`
	ByteLength           int    `json:"byte_length"`
	SourceRepository     string `json:"source_repository,omitempty"`
	SourceCommit         string `json:"source_commit,omitempty"`
	SourceVersion        string `json:"source_version,omitempty"`
	SourceArtifact       string `json:"source_artifact,omitempty"`
	SourceArtifactSHA256 string `json:"source_artifact_sha256,omitempty"`
	SourceLicenseSHA256  string `json:"source_license_sha256,omitempty"`
}

type systemPromptSourceSyncResponse struct {
	Status  service.BusinessSystemPromptSourceSyncStatus `json:"status"`
	Version *systemPromptSourceSyncVersionResponse       `json:"version,omitempty"`
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
	TemplateID           int64           `json:"template_id"`
	VersionID            int64           `json:"version_id"`
	ClientInstructions   string          `json:"client_instructions"`
	ServerInstructions   string          `json:"server_instructions"`
	Protocol             string          `json:"protocol"`
	Compact              bool            `json:"compact"`
	Body                 json.RawMessage `json:"body"`
	CompositionMode      string          `json:"composition_mode"`
	BundleID             string          `json:"bundle_id"`
	BundleManifestSHA256 string          `json:"bundle_manifest_sha256"`
	ClientMode           string          `json:"client_mode"`
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
		Enabled:                       snapshot.Enabled,
		ExposeServerPrompt:            snapshot.ExposeServerPrompt,
		CompactEnabled:                snapshot.CompactEnabled,
		TemplateID:                    snapshot.TemplateID,
		VersionID:                     snapshot.VersionID,
		TemplateVersion:               snapshot.TemplateVersion,
		Revision:                      snapshot.Revision,
		SHA256:                        strings.ToLower(snapshot.SHA256),
		ByteLength:                    snapshot.ByteLength,
		CompositionMode:               snapshot.CompositionMode,
		BundleID:                      snapshot.BundleID,
		BundleManifestSHA256:          snapshot.BundleManifestSHA256,
		RegistryRevision:              snapshot.RegistryRevision,
		RegistryRawTreeSHA256:         snapshot.RegistryRawTreeSHA256,
		RegistryEffectiveTreeSHA256:   snapshot.RegistryEffectiveTreeSHA256,
		RegistryPromptRawSHA256:       snapshot.RegistryPromptRawSHA256,
		RegistryPromptEffectiveSHA256: snapshot.RegistryPromptEffectiveSHA256,
		RegistryUpstreamSourceID:      snapshot.RegistryUpstreamSourceID,
		RegistryUpstreamRoot:          snapshot.RegistryUpstreamRoot,
		RegistryPublicRoot:            snapshot.RegistryPublicRoot,
		BundleAvailable:               snapshot.BundleAvailable,
		BundleDegraded:                snapshot.BundleDegraded,
		DegradedReason:                snapshot.DegradedReason,
		Degraded:                      snapshot.Degraded,
		UpdatedAt:                     snapshot.UpdatedAt,
	}
}

func writeBusinessSystemPromptError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrBusinessSystemPromptRevisionConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "system_prompt_revision_conflict", "system_prompt_revision_conflict", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptUnavailable):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "system_prompt_unavailable", "system_prompt_unavailable", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptSourceUnavailable):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "system_prompt_source_unavailable", "system_prompt_source_unavailable", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptSourceInvalid):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, "system_prompt_source_invalid", "system_prompt_source_invalid", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptSourceLicenseChanged):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, "system_prompt_source_license_changed", "system_prompt_source_license_changed", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptSourceNotManaged):
		response.ErrorWithDetails(c, http.StatusConflict, "system_prompt_source_not_managed", "system_prompt_source_not_managed", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptBundleInvalid):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, "remote_skill_candidate_invalid", "remote_skill_candidate_invalid", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptBundleUnavailable):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "remote_skill_source_unavailable", "remote_skill_source_unavailable", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptTemplateNotFound), errors.Is(err, service.ErrBusinessSystemPromptVersionNotFound):
		response.NotFound(c, "System prompt template or version not found")
	case errors.Is(err, service.ErrRemoteSkillVersionNotFound), errors.Is(err, service.ErrRemoteSkillSyncNotFound):
		response.NotFound(c, "Remote skill version or sync job not found")
	case errors.Is(err, service.ErrBusinessSystemPromptSeedProtected), errors.Is(err, service.ErrBusinessSystemPromptActive):
		response.ErrorWithDetails(c, http.StatusConflict, err.Error(), "system_prompt_delete_protected", nil)
	case errors.Is(err, service.ErrBusinessSystemPromptInvalid):
		response.BadRequest(c, err.Error())
	default:
		response.ErrorFrom(c, err)
	}
}

func (h *SystemPromptHandler) SkillRegistry(c *gin.Context) {
	if h.skillRegistry == nil {
		writeBusinessSystemPromptError(c, service.ErrBusinessSystemPromptUnavailable)
		return
	}
	versions, err := h.skillRegistry.ListVersions(c.Request.Context())
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, gin.H{
		"runtime": h.skillRegistry.CurrentSnapshot(), "versions": versions,
		"source": gin.H{
			"upstream_source_id": service.RemoteSkillUpstreamSourceID,
			"upstream_root":      service.RemoteSkillUpstreamRoot,
			"public_root":        service.RemoteSkillPublicRoot,
		},
	})
}

func (h *SystemPromptHandler) SkillVersions(c *gin.Context) {
	if h.skillRegistry == nil {
		writeBusinessSystemPromptError(c, service.ErrBusinessSystemPromptUnavailable)
		return
	}
	versions, err := h.skillRegistry.ListVersions(c.Request.Context())
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, versions)
}

func (h *SystemPromptHandler) SkillVersion(c *gin.Context) {
	id, ok := parsePositiveID(c, "bundle_version_id")
	if !ok {
		return
	}
	version, err := h.skillRegistry.InspectVersion(c.Request.Context(), id)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, version)
}

func (h *SystemPromptHandler) StartSkillSync(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	if h.skillRegistry == nil {
		writeBusinessSystemPromptError(c, service.ErrBusinessSystemPromptUnavailable)
		return
	}
	expectedRevision, promptCapture, err := parseRemoteSkillSyncMultipart(c)
	if err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	job, err := h.skillRegistry.StartSync(c.Request.Context(), promptCapture, actorID, expectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"upstream_source_id": service.RemoteSkillUpstreamSourceID,
		"revision":           expectedRevision,
		"prompt_uploaded":    job.PromptCaptureProvided,
		"status":             job.Status,
		"result":             "sync_queued",
	})
	response.Accepted(c, job)
}

func parseRemoteSkillSyncMultipart(c *gin.Context) (int64, []byte, error) {
	if c.ContentType() != "multipart/form-data" {
		return 0, nil, errors.New("multipart/form-data is required")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(service.BusinessSystemPromptMaxBytes+(1<<20)))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		return 0, nil, err
	}
	if c.Request.MultipartForm == nil {
		return 0, nil, errors.New("multipart form is required")
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	revisions := c.Request.MultipartForm.Value["expected_revision"]
	if len(revisions) != 1 {
		return 0, nil, errors.New("expected_revision must appear exactly once")
	}
	expectedRevision, err := strconv.ParseInt(strings.TrimSpace(revisions[0]), 10, 64)
	if err != nil || expectedRevision < 1 {
		return 0, nil, errors.New("expected_revision must be a positive integer")
	}
	files := c.Request.MultipartForm.File["prompt_capture"]
	if len(files) > 1 {
		return 0, nil, errors.New("prompt_capture must appear at most once")
	}
	if len(files) == 0 {
		return expectedRevision, nil, nil
	}
	if files[0].Size <= 0 || files[0].Size > int64(service.BusinessSystemPromptMaxBytes) {
		return 0, nil, errors.New("prompt_capture size is invalid")
	}
	stream, err := files[0].Open()
	if err != nil {
		return 0, nil, err
	}
	promptCapture, readErr := io.ReadAll(io.LimitReader(stream, int64(service.BusinessSystemPromptMaxBytes)+1))
	closeErr := stream.Close()
	if readErr != nil {
		return 0, nil, readErr
	}
	if closeErr != nil {
		return 0, nil, closeErr
	}
	if len(promptCapture) == 0 || len(promptCapture) > service.BusinessSystemPromptMaxBytes || !utf8.Valid(promptCapture) {
		return 0, nil, errors.New("prompt_capture must be non-empty UTF-8 within the size limit")
	}
	return expectedRevision, promptCapture, nil
}

func (h *SystemPromptHandler) SkillSync(c *gin.Context) {
	id, ok := parsePositiveID(c, "sync_id")
	if !ok {
		return
	}
	job, err := h.skillRegistry.GetSyncJob(c.Request.Context(), id)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	response.Success(c, job)
}

func (h *SystemPromptHandler) PublishSkillVersion(c *gin.Context) {
	h.publishSkillVersion(c, "published")
}

func (h *SystemPromptHandler) RollbackSkillVersion(c *gin.Context) {
	h.publishSkillVersion(c, "rolled_back")
}

func (h *SystemPromptHandler) publishSkillVersion(c *gin.Context, result string) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	versionID, ok := parsePositiveID(c, "bundle_version_id")
	if !ok {
		return
	}
	var req struct {
		ExpectedRevision int64 `json:"expected_revision" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	old := h.skillRegistry.CurrentSnapshot()
	snapshot, err := h.skillRegistry.PublishVersion(c.Request.Context(), versionID, req.ExpectedRevision, actorID)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	extra := map[string]any{
		"bundle_version_id": versionID, "revision": snapshot.Revision,
		"status": "active", "result": result, "degraded": snapshot.Degraded,
	}
	if old.Active != nil {
		extra["old_effective_tree_sha256"] = old.Active.EffectiveTreeSHA256
	}
	if snapshot.Active != nil {
		extra["upstream_source_id"] = snapshot.Active.UpstreamSourceID
		extra["upstream_root"] = snapshot.Active.UpstreamRoot
		extra["public_root"] = snapshot.Active.PublicRoot
		extra["raw_tree_sha256"] = snapshot.Active.RawTreeSHA256
		extra["effective_tree_sha256"] = snapshot.Active.EffectiveTreeSHA256
		extra["file_count"] = snapshot.Active.FileCount
		extra["raw_total_bytes"] = snapshot.Active.RawTotalBytes
		extra["effective_total_bytes"] = snapshot.Active.EffectiveTotalBytes
		extra["script_changes"] = snapshot.Active.ScriptChanges
		extra["binary_changes"] = snapshot.Active.BinaryChanges
	}
	if snapshot.ActivePrompt != nil {
		extra["prompt_raw_sha256"] = snapshot.ActivePrompt.RawSHA256
		extra["prompt_effective_sha256"] = snapshot.ActivePrompt.EffectiveSHA256
	}
	middleware.SetAuditExtra(c, extra)
	response.Success(c, snapshot)
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
		CompositionMode: req.CompositionMode, BundleID: req.BundleID, BundleManifestSHA256: req.BundleManifestSHA256,
	}, actorID, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	if len(created.Versions) > 0 {
		middleware.SetAuditExtra(c, map[string]any{
			"template_id": created.Template.ID, "template_version": created.Versions[0].Version,
			"new_sha256": created.Versions[0].SHA256, "byte_length": created.Versions[0].ByteLength,
			"composition_mode": created.Versions[0].CompositionMode, "bundle_id": created.Versions[0].BundleID,
			"bundle_manifest_sha256": created.Versions[0].BundleManifestSHA256,
			"revision":               req.ExpectedRevision, "result": "created",
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
	version, err := h.service.CreateVersionWithComposition(c.Request.Context(), templateID, service.BusinessSystemPromptVersionCreate{
		Body: req.Body, Note: req.Note, CompositionMode: req.CompositionMode,
		BundleID: req.BundleID, BundleManifestSHA256: req.BundleManifestSHA256,
	}, actorID, req.ExpectedLatestVersion, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": templateID, "template_version": version.Version,
		"new_sha256": version.SHA256, "byte_length": version.ByteLength,
		"composition_mode": version.CompositionMode, "bundle_id": version.BundleID,
		"bundle_manifest_sha256": version.BundleManifestSHA256,
		"revision":               req.ExpectedRevision, "result": "draft_saved",
	})
	response.Created(c, version)
}

func (h *SystemPromptHandler) SyncManagedSource(c *gin.Context) {
	actorID, ok := h.actorID(c)
	if !ok {
		return
	}
	templateID, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	var req systemPromptSourceSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.service.SyncManagedSource(c.Request.Context(), templateID, actorID, req.ExpectedLatestVersion, req.ExpectedRevision)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	audit := map[string]any{
		"template_id": templateID, "expected_latest_version": req.ExpectedLatestVersion,
		"revision": req.ExpectedRevision, "status": result.Status,
	}
	output := systemPromptSourceSyncResponse{Status: result.Status}
	if result.Version != nil {
		version := result.Version
		output.Version = &systemPromptSourceSyncVersionResponse{
			ID: version.ID, TemplateID: version.TemplateID, Version: version.Version,
			SHA256: version.SHA256, ByteLength: version.ByteLength,
			SourceRepository: version.SourceRepository, SourceCommit: version.SourceCommit,
			SourceVersion: version.SourceVersion, SourceArtifact: version.SourceArtifact,
			SourceArtifactSHA256: version.SourceArtifactSHA256, SourceLicenseSHA256: version.SourceLicenseSHA256,
		}
		audit["template_version"] = version.Version
		audit["source_commit"] = version.SourceCommit
		audit["source_artifact_sha256"] = version.SourceArtifactSHA256
	}
	middleware.SetAuditExtra(c, audit)
	response.Success(c, output)
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
		"composition_mode": snapshot.CompositionMode, "bundle_id": snapshot.BundleID,
		"bundle_manifest_sha256": snapshot.BundleManifestSHA256, "degraded": snapshot.Degraded,
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
		extra["composition_mode"] = created.Versions[0].CompositionMode
		extra["bundle_id"] = created.Versions[0].BundleID
		extra["bundle_manifest_sha256"] = created.Versions[0].BundleManifestSHA256
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
		"composition_mode": snapshot.CompositionMode, "bundle_id": snapshot.BundleID,
		"bundle_manifest_sha256": snapshot.BundleManifestSHA256, "degraded": snapshot.Degraded,
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
	composition := service.BusinessSystemPromptComposition{Mode: req.CompositionMode, BundleID: req.BundleID, BundleManifestSHA256: req.BundleManifestSHA256}
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
		composition = service.BusinessSystemPromptComposition{Mode: version.CompositionMode, BundleID: version.BundleID, BundleManifestSHA256: version.BundleManifestSHA256}
	}
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(server)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": req.TemplateID, "template_version": templateVersion,
		"new_sha256": hash, "byte_length": byteLength, "composition_mode": composition.Mode,
		"bundle_id": composition.BundleID, "bundle_manifest_sha256": composition.BundleManifestSHA256,
		"result": "previewed",
	})
	clientMode, err := normalizeSystemPromptPreviewClientMode(req.ClientMode)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	prepared, err := h.service.PrepareBusinessSystemPromptPreviewSnapshotForClient(service.BusinessSystemPromptSnapshot{
		Enabled: true, Revision: 1, Body: server, SHA256: hash, ByteLength: byteLength,
		CompositionMode: composition.Mode, BundleID: composition.BundleID, BundleManifestSHA256: composition.BundleManifestSHA256,
	}, "", clientMode)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	_, application, err := service.ApplyBusinessSystemPromptToJSON([]byte(`{"input":"preview"}`), prepared,
		service.BusinessSystemPromptTarget{Platform: service.PlatformOpenAI, Protocol: service.BusinessSystemPromptProtocolResponses})
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": req.TemplateID, "template_version": templateVersion,
		"new_sha256": hash, "byte_length": byteLength, "composition_mode": composition.Mode,
		"bundle_id": composition.BundleID, "bundle_revision": application.BundleRevision,
		"bundle_manifest_sha256": application.BundleManifestSHA256, "client_mode": clientMode,
		"result": "previewed",
	})
	response.Success(c, gin.H{
		"instructions": service.MergeBusinessSystemPromptInstructions(req.ClientInstructions, prepared.Body),
		"client_mode":  clientMode, "base_server_instructions": server, "final_server_instructions": prepared.Body,
		"application": application,
	})
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
	composition := service.BusinessSystemPromptComposition{Mode: req.CompositionMode, BundleID: req.BundleID, BundleManifestSHA256: req.BundleManifestSHA256}
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
		composition = service.BusinessSystemPromptComposition{Mode: version.CompositionMode, BundleID: version.BundleID, BundleManifestSHA256: version.BundleManifestSHA256}
	}
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(server)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": req.TemplateID, "template_version": templateVersion,
		"new_sha256": hash, "byte_length": byteLength, "composition_mode": composition.Mode,
		"bundle_id": composition.BundleID, "bundle_manifest_sha256": composition.BundleManifestSHA256,
		"result": "previewed",
	})
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		protocol = service.BusinessSystemPromptProtocolResponses
	}
	clientMode, err := normalizeSystemPromptPreviewClientMode(req.ClientMode)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	previewSnapshot, err := h.service.PrepareBusinessSystemPromptPreviewSnapshotForClient(service.BusinessSystemPromptSnapshot{
		Enabled: true, CompactEnabled: true, TemplateID: req.TemplateID, VersionID: versionID, TemplateVersion: templateVersion,
		Revision: 1, Body: server, SHA256: hash, ByteLength: byteLength,
		CompositionMode: composition.Mode, BundleID: composition.BundleID, BundleManifestSHA256: composition.BundleManifestSHA256,
	}, "", clientMode)
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	updated, application, err := service.ApplyBusinessSystemPromptToJSON(req.Body, previewSnapshot,
		service.BusinessSystemPromptTarget{Platform: service.PlatformOpenAI, Protocol: protocol, Compact: req.Compact})
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	var decoded any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		response.BadRequest(c, "preview body could not be decoded")
		return
	}
	middleware.SetAuditExtra(c, map[string]any{
		"template_id": req.TemplateID, "template_version": templateVersion,
		"new_sha256": hash, "byte_length": byteLength, "composition_mode": composition.Mode,
		"bundle_id": composition.BundleID, "bundle_revision": application.BundleRevision,
		"bundle_manifest_sha256": application.BundleManifestSHA256, "client_mode": clientMode,
		"result": "previewed",
	})
	response.Success(c, gin.H{
		"body": decoded, "application": application, "client_mode": clientMode,
		"base_server_instructions": server, "final_server_instructions": previewSnapshot.Body,
	})
}

func normalizeSystemPromptPreviewClientMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "codex", nil
	}
	if value != "codex" && value != "openai_compatible" {
		return "", service.ErrBusinessSystemPromptInvalid
	}
	return value, nil
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
