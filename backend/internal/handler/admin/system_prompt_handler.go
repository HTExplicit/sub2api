package admin

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SystemPromptHandler struct {
	service *service.BusinessSystemPromptService
}

func NewSystemPromptHandler(prompts *service.BusinessSystemPromptService) *SystemPromptHandler {
	return &SystemPromptHandler{service: prompts}
}

func (h *SystemPromptHandler) actorID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return 0, false
	}
	return subject.UserID, true
}

// Retired is routed only inside the existing authenticated admin group. No
// legacy management service is reachable from these compatibility URLs.
func (h *SystemPromptHandler) Retired(c *gin.Context) {
	response.ErrorWithDetails(c, http.StatusGone, "This prompt management endpoint is retired; use the prompt config editor", "system_prompt_management_retired", nil)
}

func (h *SystemPromptHandler) RuleHistory(c *gin.Context) {
	versions, err := h.service.PromptRuleHistory(c.Request.Context(), c.Param("rule_id"))
	if err != nil {
		writeBusinessSystemPromptError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, versions)
}

func writeBusinessSystemPromptError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPromptDeliveryUnsupported):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, "The selected prompt delivery or position is unsupported by this destination", "prompt_delivery_unsupported", nil)
	case errors.Is(err, service.ErrPromptRuleReferenced):
		response.ErrorWithDetails(c, http.StatusConflict, "An account still references this rule", "prompt_rule_referenced", nil)
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
