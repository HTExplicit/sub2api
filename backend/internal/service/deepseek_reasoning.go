package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const SettingKeyDeepSeekReasoning = "deepseek_reasoning_config"
const deepSeekReasoningSnapshotKey = "deepseek_reasoning_snapshot"

type deepSeekReasoningDefaultContextKey struct{}

func deepSeekReasoningWasDefaulted(ctx context.Context) bool {
	value, _ := ctx.Value(deepSeekReasoningDefaultContextKey{}).(bool)
	return value
}

type DeepSeekReasoningConfig struct {
	Enabled bool   `json:"enabled"`
	Effort  string `json:"effort"`
}

func DefaultDeepSeekReasoningConfig() DeepSeekReasoningConfig {
	return DeepSeekReasoningConfig{Effort: "max"}
}

func (c DeepSeekReasoningConfig) Validate() error {
	if !slices.Contains([]string{"low", "high", "max"}, c.Effort) {
		return infraerrors.BadRequest("DEEPSEEK_REASONING_INVALID", "effort must be low, high, or max")
	}
	return nil
}

// DeepSeekReasoningService publishes immutable settings snapshots. Forwarding
// reads memory only; stale snapshots refresh asynchronously, as system prompts do.
type DeepSeekReasoningService struct {
	settings    SettingRepository
	mu          sync.Mutex
	snapshot    atomic.Pointer[DeepSeekReasoningConfig]
	nextRefresh atomic.Int64
	refreshing  atomic.Bool
}

func NewDeepSeekReasoningService(settings SettingRepository) *DeepSeekReasoningService {
	return &DeepSeekReasoningService{settings: settings}
}

func ProvideDeepSeekReasoningService(settings SettingRepository) *DeepSeekReasoningService {
	s := NewDeepSeekReasoningService(settings)
	if _, err := s.Get(context.Background()); err != nil {
		slog.Warn("deepseek_reasoning.load_failed", "error", err)
	}
	return s
}

func (s *DeepSeekReasoningService) publish(config DeepSeekReasoningConfig) {
	s.snapshot.Store(&config)
	s.nextRefresh.Store(time.Now().Add(time.Minute).UnixNano())
}

func (s *DeepSeekReasoningService) Get(ctx context.Context) (DeepSeekReasoningConfig, error) {
	config := DefaultDeepSeekReasoningConfig()
	if s == nil || s.settings == nil {
		return config, infraerrors.ServiceUnavailable("DEEPSEEK_REASONING_UNAVAILABLE", "DeepSeek reasoning settings are unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := s.settings.GetValue(readCtx, SettingKeyDeepSeekReasoning)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(raw) == "") {
		err = nil
	} else if err == nil {
		err = json.Unmarshal([]byte(raw), &config)
	}
	if err == nil {
		err = config.Validate()
	}
	if err != nil {
		s.nextRefresh.Store(time.Now().Add(5 * time.Second).UnixNano())
		return config, fmt.Errorf("load DeepSeek reasoning settings: %w", err)
	}
	s.publish(config)
	return config, nil
}

func (s *DeepSeekReasoningService) Save(ctx context.Context, config DeepSeekReasoningConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if s == nil || s.settings == nil {
		return infraerrors.ServiceUnavailable("DEEPSEEK_REASONING_UNAVAILABLE", "DeepSeek reasoning settings are unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := s.settings.Set(ctx, SettingKeyDeepSeekReasoning, string(raw)); err != nil {
		return err
	}
	s.publish(config)
	return nil
}

func (s *DeepSeekReasoningService) current() DeepSeekReasoningConfig {
	if s == nil {
		return DefaultDeepSeekReasoningConfig()
	}
	if time.Now().UnixNano() >= s.nextRefresh.Load() && s.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.refreshing.Store(false)
			if _, err := s.Get(context.Background()); err != nil {
				slog.Warn("deepseek_reasoning.refresh_failed", "error", err)
			}
		}()
	}
	if value := s.snapshot.Load(); value != nil {
		return *value
	}
	return DefaultDeepSeekReasoningConfig()
}

// Freeze once per logical request, including account failovers and nested bridges.
func (s *DeepSeekReasoningService) requestConfig(c *gin.Context) DeepSeekReasoningConfig {
	if c != nil {
		if value, ok := c.Get(deepSeekReasoningSnapshotKey); ok {
			if config, ok := value.(DeepSeekReasoningConfig); ok {
				return config
			}
		}
	}
	config := s.current()
	if c != nil {
		c.Set(deepSeekReasoningSnapshotKey, config)
	}
	return config
}

func deepSeekSupportsDefaultEffort(account *Account, model, effort string) bool {
	metadataModel := strings.TrimSpace(model)
	model = strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	if !strings.HasPrefix(model, "deepseek-") {
		return false
	}
	if strings.HasPrefix(model, "deepseek-flash") || strings.HasPrefix(model, "deepseek-v4") {
		return true
	}
	if account != nil {
		if metadata, ok := account.GetUpstreamModelMetadata(metadataModel); ok {
			return (metadata.Reasoning == nil || *metadata.Reasoning) && slices.Contains(normalizeReasoningLevels(metadata.SupportedReasoningLevels), effort)
		}
	}
	return false
}

// applyDeepSeekDefault runs before adapters synthesize their own effort. It
// leaves explicit intent (including unknown/invalid values) to existing validation.
func applyDeepSeekDefault(body []byte, account *Account, model, protocol string, config DeepSeekReasoningConfig, group *Group) ([]byte, bool, error) {
	if !config.Enabled || !deepSeekSupportsDefaultEffort(account, model, config.Effort) {
		return body, false, nil
	}
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()), "disabled") {
		return body, false, nil
	}
	for _, parent := range []string{"reasoning", "output_config", "thinking"} {
		v := gjson.GetBytes(body, parent)
		if v.Exists() && v.Type != gjson.Null && !v.IsObject() {
			return body, false, nil
		}
	}
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		v := gjson.GetBytes(body, path)
		if v.Exists() && v.Type != gjson.Null && (v.Type != gjson.String || strings.TrimSpace(v.String()) != "") {
			return body, false, nil
		}
	}
	if canonicalReasoningEffortFromModelSuffix(gjson.GetBytes(body, "model").String()) != "" || canonicalReasoningEffortFromModelSuffix(model) != "" {
		return body, false, nil
	}
	path := "reasoning.effort"
	switch protocol {
	case APIProtocolChatCompletions:
		path = "reasoning_effort"
	case APIProtocolAnthropic:
		path = "output_config.effort"
	}
	updated, err := sjson.SetBytes(body, path, config.Effort)
	if err != nil {
		return body, false, err
	}
	if group != nil {
		updated, _, err = ApplyReasoningEffortPolicy(updated, group.MaxReasoningEffort, group.ReasoningEffortMappings, group.MaxReasoningEffortOverLimit)
		if err != nil {
			return body, false, err
		}
	}
	return updated, true, nil
}

func (s *DeepSeekReasoningService) prepare(c *gin.Context, account *Account, body []byte, model, protocol string) ([]byte, bool, func([]byte) ([]byte, error), error) {
	config := s.requestConfig(c)
	// Compact is a separate, non-generation operation with its own model mapping.
	if c != nil && isOpenAIResponsesCompactPath(c) {
		config.Enabled = false
	}
	var group *Group
	if c != nil {
		if key := getAPIKeyFromContext(c); key != nil && key.Group != nil {
			copy := *key.Group
			copy.ReasoningEffortMappings = slices.Clone(copy.ReasoningEffortMappings)
			group = &copy
		}
	}
	replay := func(raw []byte) ([]byte, error) {
		out, _, err := applyDeepSeekDefault(raw, account, model, protocol, config, group)
		return out, err
	}
	out, changed, err := applyDeepSeekDefault(body, account, model, protocol, config, group)
	if IsReasoningEffortPolicyDenied(err) && c != nil && c.Writer != nil && !c.Writer.Written() {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		switch protocol {
		case APIProtocolAnthropic:
			writeAnthropicError(c, 403, "permission_error", err.Error())
		case APIProtocolChatCompletions:
			writeChatCompletionsError(c, 403, "permission_error", err.Error())
		default:
			writeResponsesError(c, 403, "permission_error", err.Error())
		}
	}
	return out, changed, replay, err
}

func (s *OpenAIGatewayService) SetDeepSeekReasoningService(value *DeepSeekReasoningService) {
	s.deepseekReasoning = value
}
func (s *GatewayService) SetDeepSeekReasoningService(value *DeepSeekReasoningService) {
	s.deepseekReasoning = value
}

func requestedEffortForPersistence(requested, forwarded *string, defaulted bool) *string {
	if defaulted {
		return requested
	}
	return coalesceRequestedReasoningEffort(requested, forwarded)
}

func (s *OpenAIGatewayService) ForwardAsAnthropic(ctx context.Context, c *gin.Context, account *Account, body []byte, promptCacheKey, defaultMappedModel string) (*OpenAIForwardResult, error) {
	body, defaulted, _, err := s.deepseekReasoning.prepare(c, account, body, resolveMappedUpstreamModel(account, body, defaultMappedModel), APIProtocolAnthropic)
	if err != nil {
		return nil, err
	}
	if defaulted {
		ctx = context.WithValue(ctx, deepSeekReasoningDefaultContextKey{}, true)
	}
	result, err := s.forwardAsAnthropic(ctx, c, account, body, promptCacheKey, defaultMappedModel)
	if result != nil && defaulted {
		result.ReasoningEffortDefaulted = true
	}
	return result, err
}

func (s *GatewayService) ForwardAsResponses(ctx context.Context, c *gin.Context, account *Account, body []byte, parsed *ParsedRequest) (*ForwardResult, error) {
	body, defaulted, _, err := s.deepseekReasoning.prepare(c, account, body, account.GetMappedModel(gjson.GetBytes(body, "model").String()), APIProtocolResponses)
	if err != nil {
		return nil, err
	}
	result, err := s.forwardAsResponses(ctx, c, account, body, parsed)
	if result != nil && defaulted {
		result.ReasoningEffortDefaulted = true
	}
	return result, err
}

func (s *GatewayService) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte, parsed *ParsedRequest) (*ForwardResult, error) {
	body, defaulted, _, err := s.deepseekReasoning.prepare(c, account, body, account.GetMappedModel(gjson.GetBytes(body, "model").String()), APIProtocolChatCompletions)
	if err != nil {
		return nil, err
	}
	result, err := s.forwardAsChatCompletions(ctx, c, account, body, parsed)
	if result != nil && defaulted {
		result.ReasoningEffortDefaulted = true
	}
	return result, err
}
