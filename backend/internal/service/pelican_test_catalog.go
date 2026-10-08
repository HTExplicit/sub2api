package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	PelicanTestMaxConcurrency                  = PelicanExecutionConcurrency
	PelicanTestDefaultGenerationTimeoutSeconds = PelicanDefaultGenerationTimeoutSeconds
	PelicanTestMinGenerationTimeoutSeconds     = PelicanMinGenerationTimeoutSeconds
	PelicanTestMaxGenerationTimeoutSeconds     = PelicanMaxGenerationTimeoutSeconds
	pelicanTestCatalogMaxAccounts              = 5000
)

var ErrPelicanTestOptionsInvalidRequest = errors.New("invalid pelican test options request")

type PelicanTestModelOption struct {
	ID               string   `json:"id"`
	DisplayName      string   `json:"display_name"`
	UpstreamModel    string   `json:"upstream_model"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	DefaultEffort    string   `json:"default_effort"`
	TextSupported    bool     `json:"text_supported"`
	CapabilityReason string   `json:"capability_reason"`
}

type PelicanTestAccountOption struct {
	ID                      int64                    `json:"id"`
	Name                    string                   `json:"name"`
	Platform                string                   `json:"platform"`
	Type                    string                   `json:"type"`
	Status                  string                   `json:"status"`
	Schedulable             bool                     `json:"schedulable"`
	ParentAccountID         *int64                   `json:"parent_account_id"`
	ProxyID                 *int64                   `json:"proxy_id"`
	ProxyName               string                   `json:"proxy_name"`
	RateLimitedUntil        *time.Time               `json:"rate_limited_until"`
	OverloadUntil           *time.Time               `json:"overload_until"`
	TempUnschedulableUntil  *time.Time               `json:"temp_unschedulable_until"`
	TempUnschedulableReason string                   `json:"temp_unschedulable_reason"`
	ErrorMessage            string                   `json:"error_message"`
	CapabilityReason        string                   `json:"capability_reason"`
	ManualModelAllowed      bool                     `json:"manual_model_allowed"`
	DefaultModelID          string                   `json:"default_model_id"`
	Models                  []PelicanTestModelOption `json:"models"`
}

type PelicanTestOptions struct {
	GeneratedAt                     time.Time                  `json:"generated_at"`
	MaxConcurrency                  int                        `json:"max_concurrency"`
	DefaultGenerationTimeoutSeconds int                        `json:"default_generation_timeout_seconds"`
	MinGenerationTimeoutSeconds     int                        `json:"min_generation_timeout_seconds"`
	MaxGenerationTimeoutSeconds     int                        `json:"max_generation_timeout_seconds"`
	Accounts                        []PelicanTestAccountOption `json:"accounts"`
}

type pelicanTestCatalogAccountReader interface {
	GetByIDs(context.Context, []int64) ([]*Account, error)
}

// PelicanTestCatalog depends only on a local account reader. It cannot refresh a
// token, discover a remote catalog, prepare a borrowed route, or mutate accounts.
type PelicanTestCatalog struct {
	accounts pelicanTestCatalogAccountReader
	now      func() time.Time
}

func NewPelicanTestCatalog(accounts AccountRepository) *PelicanTestCatalog {
	return &PelicanTestCatalog{accounts: accounts, now: time.Now}
}

func (s *PelicanTestCatalog) Options(ctx context.Context, accountIDs []int64) (*PelicanTestOptions, error) {
	options := &PelicanTestOptions{
		GeneratedAt: s.now().UTC(), MaxConcurrency: PelicanTestMaxConcurrency,
		DefaultGenerationTimeoutSeconds: PelicanTestDefaultGenerationTimeoutSeconds,
		MinGenerationTimeoutSeconds:     PelicanTestMinGenerationTimeoutSeconds,
		MaxGenerationTimeoutSeconds:     PelicanTestMaxGenerationTimeoutSeconds,
		Accounts:                        make([]PelicanTestAccountOption, 0, len(accountIDs)),
	}
	if len(accountIDs) > pelicanTestCatalogMaxAccounts {
		return nil, fmt.Errorf("%w: account_ids must contain at most %d accounts", ErrPelicanTestOptionsInvalidRequest, pelicanTestCatalogMaxAccounts)
	}
	ids := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]bool, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			return nil, fmt.Errorf("%w: account_ids must be positive", ErrPelicanTestOptionsInvalidRequest)
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) == 0 {
		return options, nil
	}
	if s.accounts == nil {
		return nil, errors.New("pelican test local account reader is unavailable")
	}
	accounts, err := s.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read pelican test local accounts: %w", err)
	}
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	for _, id := range ids {
		account := byID[id]
		if account == nil {
			options.Accounts = append(options.Accounts, PelicanTestAccountOption{
				ID: id, Status: "missing", CapabilityReason: "selected account was not found", Models: []PelicanTestModelOption{},
			})
			continue
		}
		options.Accounts = append(options.Accounts, pelicanTestAccountOptions(account))
	}
	return options, nil
}

func pelicanTestAccountOptions(account *Account) PelicanTestAccountOption {
	option := PelicanTestAccountOption{
		ID: account.ID, Name: account.Name, Platform: account.Platform, Type: account.Type,
		Status: account.Status, Schedulable: account.Schedulable, ParentAccountID: account.ParentAccountID,
		ProxyID: account.ProxyID, RateLimitedUntil: account.RateLimitResetAt, OverloadUntil: account.OverloadUntil,
		TempUnschedulableUntil: account.TempUnschedulableUntil, TempUnschedulableReason: account.TempUnschedulableReason,
		ErrorMessage: account.ErrorMessage, CapabilityReason: pelicanTestAccountCapabilityReason(account),
		ManualModelAllowed: true, Models: []PelicanTestModelOption{},
	}
	if account.Proxy != nil {
		option.ProxyName = account.Proxy.Name
	}
	for _, id := range pelicanTestLocalModelIDs(account) {
		model := PelicanTestModelOptions(account, id)
		option.Models = append(option.Models, model)
		if option.DefaultModelID == "" && model.TextSupported {
			option.DefaultModelID = id
		}
	}
	if option.CapabilityReason == "" && len(option.Models) > 0 && option.DefaultModelID == "" {
		option.CapabilityReason = "the account's locally configured models do not support text generation"
	}
	return option
}

// These IDs are candidates, not evidence of a successful live model call. Exact
// saved mapping keys are retained even when they have no text capability. A
// wildcard is expanded only against local platform data and saved source-bound
// directory metadata; administrators can also enter a concrete ID themselves.
func pelicanTestLocalModelIDs(account *Account) []string {
	defaults := pelicanTestPlatformModelIDs(account)
	mapping := account.GetModelMapping()
	useMapping := len(mapping) > 0 && !account.IsOpenAIPassthroughEnabled() && (account.Platform != PlatformAntigravity || account.Type != AccountTypeUpstream)
	ids := make(map[string]bool)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || strings.ContainsAny(id, "*\x00") || len(id) > 512 {
			return
		}
		if !useMapping || account.IsModelSupported(id) {
			ids[id] = true
		}
	}
	for id := range mapping {
		add(id)
	}
	for _, id := range defaults {
		add(id)
	}
	if snapshot := account.GetUpstreamModelMetadataSnapshot(); upstreamModelMetadataSourceMatches(account, snapshot) {
		for id := range snapshot.Models {
			add(id)
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	return ordered
}

func pelicanTestPlatformModelIDs(account *Account) []string {
	switch account.Platform {
	case PlatformKimi, PlatformZhipu:
		return nil // These platforms publish no local default catalog.
	case PlatformDeepseek:
		return []string{"deepseek-v4-pro", "deepseek-v4-flash", "deepseek-flash"}
	case PlatformMiniMax:
		return []string{"MiniMax-M3", "MiniMax-M2.7", "MiniMax-M2.5"}
	case PlatformGemini:
		if account.IsGeminiGoogleOne() {
			ids := make([]string, 0, len(geminicli.GoogleOneModels))
			for _, model := range geminicli.GoogleOneModels {
				ids = append(ids, model.ID)
			}
			return ids
		}
	case PlatformOpenAI, PlatformAnthropic, PlatformAntigravity, PlatformGrok, PlatformOpenCodeGo, PlatformTypeSafe:
	default:
		return nil
	}
	return defaultModelsListCandidateIDs(account.Platform)
}

func pelicanTestAccountCapabilityReason(account *Account) string {
	if account == nil {
		return "selected account was not found"
	}
	if account.Platform == PlatformTypeSafe {
		return "TypeSafe System One provides a structured native response and does not support arbitrary HTML text generation"
	}
	switch account.Platform {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
	default:
		return fmt.Sprintf("platform %q has no text generation sender", account.Platform)
	}
	supported := false
	switch account.Platform {
	case PlatformAnthropic:
		supported = slices.Contains([]string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken, AccountTypeBedrock, AccountTypeServiceAccount}, account.Type)
	case PlatformOpenAI:
		supported = slices.Contains([]string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken}, account.Type)
	case PlatformGemini:
		supported = slices.Contains([]string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeServiceAccount}, account.Type)
	case PlatformGrok:
		supported = account.Type == AccountTypeAPIKey || account.Type == AccountTypeOAuth
	case PlatformAntigravity:
		supported = account.Type == AccountTypeOAuth || account.Type == AccountTypeUpstream
	case PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
		supported = account.Type == AccountTypeAPIKey
	}
	if !supported {
		return fmt.Sprintf("account type %q has no text generation sender for platform %q", account.Type, account.Platform)
	}
	return ""
}

func pelicanTestUpstreamModel(account *Account, model string) (string, bool) {
	if account == nil {
		return model, false
	}
	switch {
	case account.IsOpenAI():
		return resolveOpenAIAccountUpstreamModelForRequest(account, model, false), true
	case account.Platform == PlatformAntigravity && account.Type == AccountTypeUpstream:
		return model, true
	case account.Platform == PlatformAnthropic && account.IsBedrock():
		return ResolveBedrockModelID(account, model)
	case account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount:
		if mapped, matched := account.ResolveMappedModel(model); matched {
			return mapped, true
		}
		return normalizeVertexAnthropicModelID(claude.NormalizeModelID(model)), true
	case account.Platform == PlatformAnthropic && account.IsOAuth():
		return claude.NormalizeModelID(model), true
	case account.Platform == PlatformAntigravity:
		if mapped, ok := resolveGeminiThinkingVariantForLevel(account, model, ""); ok {
			return mapped, true
		}
		mapped := mapAntigravityModel(account, model)
		return mapped, mapped != ""
	case account.Platform == PlatformGemini && account.IsOAuth():
		return model, true
	default:
		return account.GetMappedModel(model), true
	}
}

func pelicanTestLocalMetadata(account *Account, model string) (UpstreamModelMetadata, bool) {
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if !upstreamModelMetadataSourceMatches(account, snapshot) {
		return UpstreamModelMetadata{}, false
	}
	metadata, ok := snapshot.Models[model]
	return metadata, ok
}

// PelicanTestTextCapabilityReason records unsupported choices without hiding an
// account, changing its configuration, or applying a group's routing policy.
func PelicanTestTextCapabilityReason(account *Account, model string) string {
	if reason := pelicanTestAccountCapabilityReason(account); reason != "" {
		return reason
	}
	model = strings.TrimSpace(model)
	if model == "" || strings.ContainsAny(model, "*\x00") || len(model) > 512 {
		return "a concrete model ID is required"
	}
	mapped, ok := pelicanTestUpstreamModel(account, model)
	if !ok || strings.TrimSpace(mapped) == "" {
		return fmt.Sprintf("model %q is unsupported by the account's native text sender", model)
	}
	// Test both IDs independently, using no second mapping of the resolved target.
	for _, id := range []string{model, mapped} {
		if !AccountTestSupportsTextConversation(&Account{Platform: PlatformOpenAI}, id) {
			return fmt.Sprintf("model %q does not support text generation", id)
		}
	}
	if metadata, known := pelicanTestLocalMetadata(account, mapped); known && len(metadata.InputModalities) > 0 && !slices.Contains(metadata.InputModalities, "text") {
		return fmt.Sprintf("model %q declares no text input capability in the account's local directory", mapped)
	}
	return ""
}

func pelicanTestUsesClaudeEffort(account *Account, model string) bool {
	if account.Platform == PlatformAnthropic {
		return true
	}
	if !claude.IsClaudeModel(model) {
		return false
	}
	if account.Platform == PlatformAntigravity && account.Type == AccountTypeUpstream {
		return true
	}
	if account.IsOpenCodeGo() {
		return openCodeGoNativeProtocol(account, model) == APIProtocolAnthropic
	}
	return account.IsCNProvider() && account.GetAPIProtocol() == APIProtocolAnthropic
}

func pelicanTestSupportsReasoningWire(account *Account, model string) bool {
	if pelicanTestUsesClaudeEffort(account, model) {
		return !account.IsBedrock() || bedrockKeepsOutputConfigEffort(model)
	}
	switch {
	case account.IsOpenAI():
		return true
	case account.IsOpenCodeGo():
		protocol := openCodeGoNativeProtocol(account, model)
		return protocol == APIProtocolChatCompletions || protocol == APIProtocolResponses
	case account.IsCNProvider():
		// A Pelican task sends one native request. Adaptive accounts do not have
		// the ordinary connection test's every-endpoint effort restriction.
		return account.GetAPIProtocol() != APIProtocolAnthropic
	case account.Platform == PlatformGrok:
		return grokSupportsReasoningEffort(accountTestGrokUpstreamModel(model))
	default:
		return false
	}
}

// PelicanTestModelOptions uses the account's actual native mapping and locally
// saved capability metadata. An omitted effort keeps the platform's own default.
func PelicanTestModelOptions(account *Account, model string) PelicanTestModelOption {
	model = strings.TrimSpace(model)
	mapped, _ := pelicanTestUpstreamModel(account, model)
	option := PelicanTestModelOption{ID: model, DisplayName: model, UpstreamModel: mapped, ReasoningEfforts: []string{}}
	option.CapabilityReason = PelicanTestTextCapabilityReason(account, model)
	option.TextSupported = option.CapabilityReason == ""
	if account == nil {
		return option
	}
	metadata, known := pelicanTestLocalMetadata(account, mapped)
	if known && mapped == model && strings.TrimSpace(metadata.DisplayName) != "" {
		option.DisplayName = metadata.DisplayName
	} else if mapped == model {
		for _, local := range openai.DefaultModels {
			if local.ID == model {
				option.DisplayName = local.DisplayName
				break
			}
		}
	}
	if !option.TextSupported || !pelicanTestSupportsReasoningWire(account, mapped) {
		return option
	}
	if pelicanTestUsesClaudeEffort(account, mapped) {
		if !known || metadata.Reasoning == nil || *metadata.Reasoning {
			option.ReasoningEfforts = claude.EffortLevelsForModel(mapped)
		}
	} else if known && (metadata.Reasoning != nil || len(metadata.SupportedReasoningLevels) > 0) {
		if metadata.Reasoning == nil || *metadata.Reasoning {
			option.ReasoningEfforts = normalizeReasoningLevels(metadata.SupportedReasoningLevels)
			if isOpenAIGPT6SolOrLunaModel(mapped) {
				option.ReasoningEfforts = intersectOrderedStrings(option.ReasoningEfforts, openai.GPT6APIReasoningEfforts())
			}
		}
	} else if account.IsOpenAIApiKey() && isOpenAIGPT6SolOrLunaModel(mapped) {
		option.ReasoningEfforts = openai.GPT6APIReasoningEfforts()
	} else {
		var levels []configuredCodexReasoningLevel
		switch {
		case account.Platform == PlatformGrok:
			levels = configuredCodexGrokReasoningLevels(accountTestGrokUpstreamModel(mapped))
		case account.IsOpenAI() && isOpenAICodexReasoningGPTModel(mapped):
			levels = configuredCodexGPTReasoningLevels(mapped)
		}
		for _, level := range levels {
			if level.Effort != "ultra" {
				option.ReasoningEfforts = append(option.ReasoningEfforts, level.Effort)
			}
		}
	}
	if option.ReasoningEfforts == nil {
		option.ReasoningEfforts = []string{}
	}
	if slices.Contains(option.ReasoningEfforts, "high") {
		option.DefaultEffort = "high"
	}
	return option
}
