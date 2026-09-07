//go:build reasoning_fidelity

package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ReasoningFidelityGatewayForTest exposes only in-memory dependency wiring to
// the external test package. This file is excluded from application builds.
// No repository that can write account, billing, or session state is attached.
type ReasoningFidelityPublicationSnapshot struct {
	Revision      int64                    `json:"revision"`
	Version       RemoteSkillBundleVersion `json:"version"`
	Prompt        RemoteSkillPromptVersion `json:"prompt"`
	RawBody       string                   `json:"raw_body"`
	EffectiveBody string                   `json:"effective_body"`
}

func ReasoningFidelityGatewayForTest(cfg *config.Config, upstream HTTPUpstream, prompt BusinessSystemPromptSnapshot, paired *ReasoningFidelityPublicationSnapshot, settings map[string]string) (*OpenAIGatewayService, error) {
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	// Runtime settings use the same getters against an immutable, read-only
	// snapshot. Missing rows retain their real defaults, not invented values.
	frozen := &reasoningFidelitySettingStore{values: make(map[string]string, len(settings))}
	for key, value := range settings {
		frozen.values[key] = value
	}
	svc.settingService = NewSettingService(frozen, cfg)
	if prompt.Enabled {
		policy := NewBusinessSystemPromptService(nil, nil)
		if prompt.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid {
			if paired == nil || paired.Revision < 1 || paired.Version.ID < 1 || paired.Prompt.ID < 1 || paired.Version.PromptVersionID != paired.Prompt.ID ||
				paired.Version.UpstreamSourceID != RemoteSkillUpstreamSourceID || paired.Version.UpstreamRoot != RemoteSkillUpstreamRoot || paired.Version.PublicRoot != RemoteSkillPublicRoot ||
				!validRemoteSkillSHA256(paired.Version.RawTreeSHA256) || !validRemoteSkillSHA256(paired.Version.EffectiveTreeSHA256) ||
				strings.TrimSpace(paired.RawBody) == "" || strings.TrimSpace(paired.EffectiveBody) == "" ||
				hashBusinessSystemPromptBundleBytes([]byte(paired.RawBody)) != paired.Prompt.RawSHA256 || hashBusinessSystemPromptBundleBytes([]byte(paired.EffectiveBody)) != paired.Prompt.EffectiveSHA256 {
				return nil, errors.New("invalid_frozen_paired_publication")
			}
			publication := &RemoteSkillPublication{Revision: paired.Revision, CandidateID: paired.Version.ID,
				EffectiveTreeSHA256: paired.Version.EffectiveTreeSHA256, EffectivePromptSHA256: paired.Prompt.EffectiveSHA256,
				EffectivePromptBody: paired.EffectiveBody, RawPromptBody: paired.RawBody, Version: paired.Version, Prompt: paired.Prompt}
			// Only the prompt component is consumed by Forward. No file download,
			// candidate install, seed, runtime mutation, or background reload runs.
			registry := NewRemoteSkillRegistryService(nil, nil, nil, nil)
			registry.publication.Store(publication)
			registry.snapshot.Store(&RemoteSkillRegistrySnapshot{Revision: paired.Revision, Active: &publication.Version, ActivePrompt: &publication.Prompt})
			policy.SetRemoteSkillRegistryService(registry)
		}
		if err := policy.prepareBusinessSystemPromptSnapshot(&prompt); err != nil {
			return nil, errors.New("invalid_frozen_prompt")
		}
		policy.snapshot.Store(&prompt)
		svc.SetBusinessSystemPromptService(policy)
	}
	return svc, nil
}

type reasoningFidelitySettingStore struct{ values map[string]string }

func (r *reasoningFidelitySettingStore) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}
func (r *reasoningFidelitySettingStore) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (r *reasoningFidelitySettingStore) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}
func (r *reasoningFidelitySettingStore) GetAll(context.Context) (map[string]string, error) {
	result := map[string]string{}
	for key, value := range r.values {
		result[key] = value
	}
	return result, nil
}
func (*reasoningFidelitySettingStore) Set(context.Context, string, string) error {
	return errors.New("diagnostic_read_only")
}
func (*reasoningFidelitySettingStore) SetMultiple(context.Context, map[string]string) error {
	return errors.New("diagnostic_read_only")
}
func (*reasoningFidelitySettingStore) Delete(context.Context, string) error {
	return errors.New("diagnostic_read_only")
}

// ReasoningFidelityContextForTest reproduces the trusted group policy context
// without authentication, scheduling, or a production API key.
func ReasoningFidelityContextForTest(ctx context.Context, c *gin.Context, group *Group, policy *OpenAIFastPolicySettings, userID int64, effort string) context.Context {
	if policy == nil {
		policy = DefaultOpenAIFastPolicySettings()
	}
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.UserID, userID)
	ctx = withOpenAIFastPolicyContext(ctx, policy)
	ctx = WithRequestedReasoningEffort(ctx, effort)
	ctx = WithOpenAIReasoningEffortPolicy(ctx, group.MaxReasoningEffort, group.ReasoningEffortMappings, group.MaxReasoningEffortOverLimit)
	c.Request = c.Request.WithContext(ctx)
	c.Set("api_key", &APIKey{ID: -1, UserID: userID, GroupID: &group.ID, Group: group})
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return ctx
}

// ReasoningFidelityDirectRequestForTest bypasses Forward's protocol/body
// transformations while using the same admin policy, endpoint construction,
// authentication and configured header overrides as the gateway arm.
func ReasoningFidelityDirectRequestForTest(ctx context.Context, svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*http.Request, error) {
	model := gjson.GetBytes(body, "model").String()
	body = ReplaceModelInBody(body, account.GetMappedModel(model))
	var err error
	// The native handler applies group effort policy before channel/account
	// mapping. The common harness ingress already performs it exactly once for
	// both arms; repeating it here could apply a chained mapping twice.
	body, err = svc.applyOpenAIFastPolicyToBody(ctx, account, gjson.GetBytes(body, "model").String(), body)
	if err != nil {
		return nil, err
	}
	body, application, err := svc.applyBusinessSystemPromptForRequest(c, body, account, BusinessSystemPromptProtocolResponses, false)
	if err != nil {
		return nil, err
	}
	if application.Applied {
		body, err = rewriteBusinessSystemPromptCacheKey(c, body, application)
		if err != nil {
			return nil, err
		}
	}
	token, _, err := svc.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return svc.buildUpstreamRequest(ctx, c, account, body, token, true, gjson.GetBytes(body, "prompt_cache_key").String(), false)
}
