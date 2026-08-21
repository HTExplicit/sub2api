package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type imageStudioJobService interface {
	EligibleKeys(ctx context.Context, userID int64) ([]service.ImageStudioEligibleKey, error)
	Create(ctx context.Context, userID int64, input service.ImageStudioCreateInput, reference, mask *service.ImageStudioUpload) (*service.ImageStudioJob, error)
	Get(ctx context.Context, userID, jobID int64) (*service.ImageStudioJob, error)
	List(ctx context.Context, userID int64, limit, offset int) ([]service.ImageStudioJob, error)
	ListItems(ctx context.Context, userID, jobID int64) ([]service.ImageStudioItem, error)
	ListArtifacts(ctx context.Context, userID, jobID int64) ([]service.ImageStudioArtifact, error)
	Cancel(ctx context.Context, userID, jobID int64) (*service.ImageStudioJob, error)
	Retry(ctx context.Context, userID, jobID int64) (*service.ImageStudioJob, error)
	OpenArtifact(ctx context.Context, userID, jobID, artifactID int64) (*service.ImageStudioArtifactDownload, error)
}

type ImageStudioJobHandler struct {
	studio imageStudioJobService
}

func NewImageStudioJobHandler(studio *service.ImageStudioService) *ImageStudioJobHandler {
	return &ImageStudioJobHandler{studio: studio}
}

func newImageStudioJobHandlerForTest(studio imageStudioJobService) *ImageStudioJobHandler {
	return &ImageStudioJobHandler{studio: studio}
}

func (h *ImageStudioJobHandler) EligibleKeys(c *gin.Context) {
	userID, ok := h.authorize(c)
	if !ok {
		return
	}
	items, err := h.studio.EligibleKeys(c.Request.Context(), userID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *ImageStudioJobHandler) Create(c *gin.Context) {
	userID, ok := h.authorize(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*service.ImageStudioMaxImageBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(2 * service.ImageStudioMaxImageBytes); err != nil {
		imageStudioJobError(c, &service.ImageStudioError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Image Studio request is invalid"})
		return
	}
	apiKeyID, err := strconv.ParseInt(strings.TrimSpace(c.PostForm("api_key_id")), 10, 64)
	if err != nil {
		apiKeyID = 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(c.PostForm("count")))
	if err != nil {
		count = 0
	}
	reference, err := imageStudioUpload(c, "reference")
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	mask, err := imageStudioUpload(c, "mask")
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	job, err := h.studio.Create(c.Request.Context(), userID, service.ImageStudioCreateInput{
		APIKeyID: apiKeyID,
		Mode:     service.ImageStudioMode(c.PostForm("mode")),
		Model:    c.PostForm("model"),
		Prompt:   c.PostForm("prompt"),
		Count:    count,
		Size:     c.PostForm("size"),
		Quality:  c.PostForm("quality"),
	}, reference, mask)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Accepted(c, job)
}

func (h *ImageStudioJobHandler) List(c *gin.Context) {
	userID, ok := h.authorize(c)
	if !ok {
		return
	}
	limit := parseImageStudioPositiveInt(c.Query("limit"), 20)
	if limit > 100 {
		limit = 100
	}
	offset := parseImageStudioPositiveInt(c.Query("offset"), 0)
	jobs, err := h.studio.List(c.Request.Context(), userID, limit, offset)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Success(c, gin.H{"items": jobs})
}

func (h *ImageStudioJobHandler) Get(c *gin.Context) {
	userID, jobID, ok := h.ownerAndJobID(c)
	if !ok {
		return
	}
	job, err := h.studio.Get(c.Request.Context(), userID, jobID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	items, err := h.studio.ListItems(c.Request.Context(), userID, jobID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	artifacts, err := h.studio.ListArtifacts(c.Request.Context(), userID, jobID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Success(c, gin.H{"job": job, "items": items, "artifacts": imageStudioArtifactViews(jobID, artifacts)})
}

func (h *ImageStudioJobHandler) Items(c *gin.Context) {
	userID, jobID, ok := h.ownerAndJobID(c)
	if !ok {
		return
	}
	items, err := h.studio.ListItems(c.Request.Context(), userID, jobID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *ImageStudioJobHandler) Cancel(c *gin.Context) {
	userID, jobID, ok := h.ownerAndJobID(c)
	if !ok {
		return
	}
	job, err := h.studio.Cancel(c.Request.Context(), userID, jobID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Success(c, job)
}

func (h *ImageStudioJobHandler) Retry(c *gin.Context) {
	userID, jobID, ok := h.ownerAndJobID(c)
	if !ok {
		return
	}
	job, err := h.studio.Retry(c.Request.Context(), userID, jobID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	response.Accepted(c, job)
}

func (h *ImageStudioJobHandler) Artifact(c *gin.Context) {
	userID, jobID, ok := h.ownerAndJobID(c)
	if !ok {
		return
	}
	artifactID, err := strconv.ParseInt(c.Param("artifact_id"), 10, 64)
	if err != nil || artifactID <= 0 {
		response.NotFound(c, "Image Studio artifact was not found")
		return
	}
	download, err := h.studio.OpenArtifact(c.Request.Context(), userID, jobID, artifactID)
	if err != nil {
		imageStudioJobError(c, err)
		return
	}
	defer func() { _ = download.Reader.Close() }()
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Type", download.Artifact.ContentType)
	c.Header("Content-Length", strconv.FormatInt(download.Artifact.ByteSize, 10))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="image-studio-%d%s"`, artifactID, imageStudioFileExtension(download.Artifact.ContentType)))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, download.Reader)
}

func (h *ImageStudioJobHandler) authorize(c *gin.Context) (int64, bool) {
	if !service.ImageStudioFeatureEnabled() {
		response.NotFound(c, "Image Studio is not enabled")
		return 0, false
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	if h == nil || h.studio == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image Studio is unavailable")
		return 0, false
	}
	return subject.UserID, true
}

func (h *ImageStudioJobHandler) ownerAndJobID(c *gin.Context) (int64, int64, bool) {
	userID, ok := h.authorize(c)
	if !ok {
		return 0, 0, false
	}
	jobID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || jobID <= 0 {
		response.NotFound(c, "Image Studio job was not found")
		return 0, 0, false
	}
	return userID, jobID, true
}

func imageStudioUpload(c *gin.Context, field string) (*service.ImageStudioUpload, error) {
	header, err := c.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil || header == nil || header.Size <= 0 || header.Size > service.ImageStudioMaxImageBytes {
		return nil, &service.ImageStudioError{Status: http.StatusBadRequest, Code: "invalid_image", Message: "Image is empty or exceeds the 20 MB limit"}
	}
	file, err := header.Open()
	if err != nil {
		return nil, &service.ImageStudioError{Status: http.StatusBadRequest, Code: "invalid_image", Message: "Image is unavailable"}
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, service.ImageStudioMaxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > service.ImageStudioMaxImageBytes {
		return nil, &service.ImageStudioError{Status: http.StatusBadRequest, Code: "invalid_image", Message: "Image is empty or exceeds the 20 MB limit"}
	}
	return &service.ImageStudioUpload{Data: data, ContentType: header.Header.Get("Content-Type")}, nil
}

func parseImageStudioPositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

type imageStudioArtifactView struct {
	service.ImageStudioArtifact
	DownloadURL string `json:"download_url"`
}

func imageStudioArtifactViews(jobID int64, artifacts []service.ImageStudioArtifact) []imageStudioArtifactView {
	result := make([]imageStudioArtifactView, 0, len(artifacts))
	for i := range artifacts {
		result = append(result, imageStudioArtifactView{
			ImageStudioArtifact: artifacts[i],
			DownloadURL:         fmt.Sprintf("/api/v1/image-studio/jobs/%d/artifacts/%d", jobID, artifacts[i].ID),
		})
	}
	return result
}

func imageStudioFileExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func imageStudioJobError(c *gin.Context, err error) {
	var studioErr *service.ImageStudioError
	if errors.As(err, &studioErr) && studioErr != nil {
		response.ErrorWithDetails(c, studioErr.Status, studioErr.Message, studioErr.Code, nil)
		return
	}
	switch {
	case errors.Is(err, service.ErrImageStudioNotFound):
		response.NotFound(c, "Image Studio job was not found")
	case errors.Is(err, service.ErrImageStudioActiveJob):
		response.ErrorWithDetails(c, http.StatusConflict, "An Image Studio job is already active", "active_job_exists", nil)
	case errors.Is(err, service.ErrImageStudioNotRetryable):
		response.ErrorWithDetails(c, http.StatusConflict, "Image Studio job cannot be retried", "job_not_retryable", nil)
	case errors.Is(err, service.ErrImageStudioRequestExpired):
		response.ErrorWithDetails(c, http.StatusConflict, "Image Studio input has expired", "input_expired", nil)
	default:
		response.InternalError(c, "Image Studio request failed")
	}
}
