package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type cindyV4CatalogFixture struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Models        []cindyV4FixtureModel `json:"models"`
}

type cindyV4FixtureModel struct {
	ID                                        string                         `json:"id"`
	Mode                                      string                         `json:"mode"`
	Name                                      string                         `json:"name"`
	Description                               string                         `json:"description"`
	ContextWindow                             *int                           `json:"contextWindow"`
	MaxOutputTokens                           *int                           `json:"maxOutputTokens"`
	Agents                                    []string                       `json:"agents"`
	Efforts                                   []string                       `json:"efforts"`
	DefaultEffort                             string                         `json:"defaultEffort"`
	Modalities                                cindyV4FixtureModalities       `json:"modalities"`
	PerAgent                                  map[string]cindyV4FixtureAgent `json:"perAgent"`
	CostDiscount                              *float64                       `json:"costDiscount"`
	InputCostPerToken                         *float64                       `json:"inputCostPerToken"`
	OutputCostPerToken                        *float64                       `json:"outputCostPerToken"`
	InputCostPerTokenPriority                 *float64                       `json:"inputCostPerTokenPriority"`
	OutputCostPerTokenPriority                *float64                       `json:"outputCostPerTokenPriority"`
	CacheReadInputTokenCost                   *float64                       `json:"cacheReadInputTokenCost"`
	CacheReadInputTokenCostPriority           *float64                       `json:"cacheReadInputTokenCostPriority"`
	CacheCreationInputTokenCost               *float64                       `json:"cacheCreationInputTokenCost"`
	InputCostPerAudioToken                    *float64                       `json:"inputCostPerAudioToken"`
	InputCostPerTokenAbove200kTokens          *float64                       `json:"inputCostPerTokenAbove200kTokens"`
	OutputCostPerTokenAbove200kTokens         *float64                       `json:"outputCostPerTokenAbove200kTokens"`
	CacheReadInputTokenCostAbove200kTokens    *float64                       `json:"cacheReadInputTokenCostAbove200kTokens"`
	InputCostPerTokenAbove272kTokens          *float64                       `json:"inputCostPerTokenAbove272kTokens"`
	OutputCostPerTokenAbove272kTokens         *float64                       `json:"outputCostPerTokenAbove272kTokens"`
	CacheReadInputTokenCostAbove272kTokens    *float64                       `json:"cacheReadInputTokenCostAbove272kTokens"`
	InputCostPerTokenAbove272kTokensPriority  *float64                       `json:"inputCostPerTokenAbove272kTokensPriority"`
	OutputCostPerTokenAbove272kTokensPriority *float64                       `json:"outputCostPerTokenAbove272kTokensPriority"`
	CacheReadAbove272kTokensPriority          *float64                       `json:"cacheReadInputTokenCostAbove272kTokensPriority"`
	InputCostPerImage                         *float64                       `json:"inputCostPerImage"`
	OutputCostPerImage                        *float64                       `json:"outputCostPerImage"`
	InputCostPerImageToken                    *float64                       `json:"inputCostPerImageToken"`
	OutputCostPerImageToken                   *float64                       `json:"outputCostPerImageToken"`
}

type cindyV4FixtureModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type cindyV4FixtureAgent struct {
	WireProtocol  string   `json:"wireProtocol"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort"`
}

func TestCindyV4CatalogFixtureMatchesPinnedSourceAndGoTable(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/cindy_model_access_v4_2026-08-24.json")
	require.NoError(t, err)
	// The public response was minified without a trailing newline. Git keeps one
	// repository newline; exclude only that byte when validating the source hash.
	payload := bytes.TrimSuffix(raw, []byte("\n"))
	payload = bytes.TrimSuffix(payload, []byte("\r"))
	sum := sha256.Sum256(payload)
	require.Equal(t, CindyModelMetadataSourceSHA256, hex.EncodeToString(sum[:]))

	var fixture cindyV4CatalogFixture
	require.NoError(t, json.Unmarshal(payload, &fixture))
	require.Equal(t, CindyModelMetadataSchemaVersion, fixture.SchemaVersion)
	require.Len(t, fixture.Models, 22)

	capabilities := CindyCapabilities()
	require.Len(t, capabilities, len(fixture.Models))
	byRegistryID := make(map[string]CindyCapability, len(capabilities))
	for _, capability := range capabilities {
		byRegistryID[capability.RegistryID] = capability
	}

	for _, model := range fixture.Models {
		model := model
		t.Run(model.ID, func(t *testing.T) {
			capability, ok := byRegistryID[model.ID]
			require.True(t, ok)
			_, publicID, ok := strings.Cut(model.ID, "/")
			require.True(t, ok)
			require.Equal(t, publicID, capability.PublicID)
			require.Equal(t, model.ID, capability.LiveUpstreamID)
			require.Equal(t, model.Name, capability.DisplayName)
			require.Equal(t, model.Description, capability.Description)
			require.Equal(t, fixtureInt(model.ContextWindow), capability.MaxInputTokens)
			require.Equal(t, fixtureInt(model.ContextWindow), capability.EffectiveCodexContextWindow())
			require.Equal(t, fixtureInt(model.MaxOutputTokens), capability.MaxOutputTokens)
			require.Equal(t, model.Modalities.Input, capability.InputModalities)
			require.Equal(t, model.Modalities.Output, capability.OutputModalities)
			require.Equal(t, model.Efforts, capability.ReasoningEfforts)
			require.Equal(t, model.DefaultEffort, capability.DefaultReasoningEffort)
			require.Equal(t, fixtureFloat(model.CostDiscount), capability.CostDiscount)

			wireProtocols := make(map[string]string, len(model.Agents))
			for _, agent := range model.Agents {
				wireProtocols[agent] = model.PerAgent[agent].WireProtocol
			}
			if len(wireProtocols) == 0 {
				require.Empty(t, capability.AgentWireProtocols)
			} else {
				require.Equal(t, wireProtocols, capability.AgentWireProtocols)
			}
			codexEfforts := model.Efforts
			if override := model.PerAgent["codex"].Efforts; len(override) > 0 {
				codexEfforts = override
			}
			require.Equal(t, codexEfforts, capability.CodexReasoningEfforts())
			if override := model.PerAgent["codex"].DefaultEffort; override != "" {
				require.Equal(t, model.DefaultEffort, override)
			}

			switch model.Mode {
			case "chat":
				require.Equal(t, CindyModelKindText, capability.Kind)
				require.NotNil(t, capability.TextPricing)
				assertCindyV4TextPricingMatchesFixture(t, model, *capability.TextPricing)
			case "image_generation":
				require.Equal(t, CindyModelKindImage, capability.Kind)
				require.NotNil(t, capability.ImagePricing)
				assertCindyV4ImagePricingMatchesFixture(t, model, *capability.ImagePricing)
			default:
				t.Fatalf("unexpected fixture mode %q", model.Mode)
			}
		})
	}
}

func assertCindyV4TextPricingMatchesFixture(t *testing.T, model cindyV4FixtureModel, pricing CindyTextPricing) {
	t.Helper()
	require.Equal(t, fixtureFloat(model.InputCostPerToken), pricing.InputCostPerToken)
	require.Equal(t, fixtureFloat(model.OutputCostPerToken), pricing.OutputCostPerToken)
	require.Equal(t, fixtureFloat(model.InputCostPerTokenPriority), pricing.InputCostPerTokenPriority)
	require.Equal(t, fixtureFloat(model.OutputCostPerTokenPriority), pricing.OutputCostPerTokenPriority)
	require.Equal(t, fixtureFloat(model.CacheReadInputTokenCost), pricing.CacheReadInputTokenCost)
	require.Equal(t, fixtureFloat(model.CacheReadInputTokenCostPriority), pricing.CacheReadInputTokenCostPriority)
	require.Equal(t, fixtureFloat(model.CacheCreationInputTokenCost), pricing.CacheCreationInputTokenCost)
	require.Equal(t, model.CacheCreationInputTokenCost != nil, pricing.CacheCreationInputTokenCostPresent)
	require.Equal(t, fixtureFloat(model.InputCostPerAudioToken), pricing.InputCostPerAudioToken)

	threshold := 0
	longInput := model.InputCostPerTokenAbove200kTokens
	longOutput := model.OutputCostPerTokenAbove200kTokens
	longCache := model.CacheReadInputTokenCostAbove200kTokens
	if fixtureAny(model.InputCostPerTokenAbove272kTokens, model.OutputCostPerTokenAbove272kTokens, model.CacheReadInputTokenCostAbove272kTokens) {
		threshold = 272000
		longInput = model.InputCostPerTokenAbove272kTokens
		longOutput = model.OutputCostPerTokenAbove272kTokens
		longCache = model.CacheReadInputTokenCostAbove272kTokens
	} else if fixtureAny(longInput, longOutput, longCache) {
		threshold = 200000
	}
	require.Equal(t, threshold, pricing.LongContextInputTokenThreshold)
	require.False(t, pricing.LongContextThresholdInclusive)
	require.Equal(t, fixtureFloat(longInput), pricing.LongContextInputCostPerToken)
	require.Equal(t, fixtureFloat(longOutput), pricing.LongContextOutputCostPerToken)
	require.Equal(t, fixtureFloat(longCache), pricing.LongContextCacheReadInputTokenCost)
	require.Equal(t, fixtureFloat(model.InputCostPerTokenAbove272kTokensPriority), pricing.LongContextInputCostPerTokenPriority)
	require.Equal(t, fixtureFloat(model.OutputCostPerTokenAbove272kTokensPriority), pricing.LongContextOutputCostPerTokenPriority)
	require.Equal(t, fixtureFloat(model.CacheReadAbove272kTokensPriority), pricing.LongContextCacheReadInputTokenCostPriority)
}

func assertCindyV4ImagePricingMatchesFixture(t *testing.T, model cindyV4FixtureModel, pricing CindyImagePricing) {
	t.Helper()
	require.Equal(t, fixtureFloat(model.InputCostPerToken), pricing.InputCostPerToken)
	require.Equal(t, fixtureFloat(model.OutputCostPerToken), pricing.OutputCostPerToken)
	require.Equal(t, fixtureFloat(model.CacheReadInputTokenCost), pricing.CacheReadInputTokenCost)
	require.Equal(t, fixtureFloat(model.InputCostPerImage), pricing.InputCostPerImage)
	require.Equal(t, fixtureFloat(model.OutputCostPerImage), pricing.OutputCostPerImage)
	require.Equal(t, fixtureFloat(model.InputCostPerImageToken), pricing.InputCostPerImageToken)
	require.Equal(t, fixtureFloat(model.OutputCostPerImageToken), pricing.OutputCostPerImageToken)
}

func fixtureAny(values ...*float64) bool {
	for _, value := range values {
		if value != nil {
			return true
		}
	}
	return false
}

func fixtureFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func fixtureInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
