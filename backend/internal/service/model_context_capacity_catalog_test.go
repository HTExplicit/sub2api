package service

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOfficialModelContextCapacityCatalogIntegrity(t *testing.T) {
	t.Parallel()

	officialHosts := map[string][]string{
		"openai":    {"developers.openai.com", "github.com"},
		"anthropic": {"platform.claude.com", "www-cdn.anthropic.com"},
		"gemini":    {"ai.google.dev"},
		"grok":      {"docs.x.ai"},
		"deepseek":  {"api-docs.deepseek.com"},
		"qwen":      {"docs.modelstudio.console.alibabacloud.com"},
		"kimi":      {"www.kimi.ai", "platform.kimi.com", "platform.kimi.ai", "www.kimi.com"},
		"zhipu":     {"docs.bigmodel.cn", "docs.z.ai"},
		"minimax":   {"platform.minimax.io", "platform.minimaxi.com"},
		"doubao":    {"www.volcengine.com"},
	}
	providers := make(map[string]bool)
	seenIDs := make(map[string]string)
	for _, row := range officialModelContextCapacityCatalog {
		rowName := row.Provider + "/" + row.Product + "/" + row.ModelID
		t.Run(rowName, func(t *testing.T) {
			for name, value := range map[string]string{
				"model ID":       row.ModelID,
				"provider":       row.Provider,
				"product":        row.Product,
				"source URL":     row.SourceURL,
				"verified date":  row.VerifiedAt,
				"original text":  row.OriginalText,
				"capacity basis": row.CapacityBasis,
			} {
				if value == "" || strings.TrimSpace(value) != value {
					t.Errorf("%s must be nonempty and have no surrounding whitespace: %q", name, value)
				}
			}
			if _, err := time.Parse("2006-01-02", row.VerifiedAt); err != nil {
				t.Errorf("invalid verified date %q", row.VerifiedAt)
			}
			if row.ContextWindow < 0 || row.MaxContextWindow < 0 || row.MaxInputTokens < 0 || row.MaxOutputTokens < 0 {
				t.Errorf("capacity fields must not be negative: %+v", row.ModelContextCapacity)
			}
			switch row.CapacityBasis {
			case "total_context":
				if row.ContextWindow <= 0 {
					t.Errorf("total-context record needs a positive context window: %+v", row.ModelContextCapacity)
				}
			case "input_limit":
				if row.ContextWindow != 0 || row.MaxInputTokens <= 0 {
					t.Errorf("input-only record must not invent a total context window: %+v", row.ModelContextCapacity)
				}
			case ModelContextCapacityBasisMaximum:
				if row.ContextWindow != 0 || row.MaxContextWindow <= 0 {
					t.Errorf("maximum-only reference must identify its explicit maximum: %+v", row.ModelContextCapacity)
				}
			default:
				t.Errorf("unsupported official capacity basis %q", row.CapacityBasis)
			}
			if row.MaxContextWindow > 0 && row.MaxContextWindow < row.ContextWindow {
				t.Errorf("maximum context %d is below context %d", row.MaxContextWindow, row.ContextWindow)
			}

			allowedHosts, knownProvider := officialHosts[row.Provider]
			if !knownProvider {
				t.Errorf("unexpected provider %q", row.Provider)
			}
			for _, source := range append([]string{row.SourceURL}, row.SourceURLs...) {
				parsed, err := url.Parse(source)
				if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
					t.Errorf("source must be an official HTTPS page URL: %q", source)
					continue
				}
				if parsed.Port() != "" || !officialCapacityCatalogTestContains(allowedHosts, parsed.Hostname()) {
					t.Errorf("source host %q is not an approved official source for %s", parsed.Host, row.Provider)
				}
				if parsed.Hostname() == "github.com" && source != gptContextCapacityReferenceSource {
					t.Errorf("GitHub reference must be the pinned official Codex catalog: %q", source)
				}
			}
			if ref := row.Reference; ref != nil {
				if ref.Product != "codex_subscription" || ref.SourceURL != gptContextCapacityReferenceSource ||
					ref.Release != GPTContextCapacityReferenceRelease || ref.VerifiedAt != gptContextCapacityReferenceVerifiedAt ||
					ref.ContextWindow <= 0 || ref.MaxContextWindow < ref.ContextWindow || row.Conditions == "" {
					t.Errorf("invalid or unexplained Codex reference: %+v", ref)
				}
			}
		})
		providers[row.Provider] = true
		for _, id := range append([]string{row.ModelID}, row.Aliases...) {
			if id == "" || strings.TrimSpace(id) != id || strings.ContainsAny(id, "*?") {
				t.Errorf("%s: model IDs and aliases must be exact nonempty names, got %q", rowName, id)
			}
			key := row.Provider + "\x00" + row.Product + "\x00" + id
			if previous, duplicate := seenIDs[key]; duplicate {
				t.Errorf("duplicate provider/product/model-or-alias %q in %s and %s", id, previous, rowName)
			}
			seenIDs[key] = rowName
		}
	}
	if len(providers) != len(officialHosts) {
		t.Errorf("catalog has %d providers, want exactly %d", len(providers), len(officialHosts))
	}
	for provider := range officialHosts {
		if !providers[provider] {
			t.Errorf("catalog is missing provider %q", provider)
		}
	}
}

func TestOfficialModelContextCapacityCatalogPreservesProviderSemantics(t *testing.T) {
	t.Parallel()

	foundCodingK3 := false
	for _, row := range officialModelContextCapacityCatalog {
		rowName := row.Provider + "/" + row.Product + "/" + row.ModelID
		t.Run(rowName, func(t *testing.T) {
			switch row.Provider {
			case "gemini":
				if row.CapacityBasis != "input_limit" || row.ContextWindow != 0 || row.MaxContextWindow != 0 || row.MaxInputTokens != 1048576 {
					t.Errorf("Gemini must retain its published input-only window: %+v", row.ModelContextCapacity)
				}
				wantOutput := int64(65536)
				if strings.HasPrefix(row.ModelID, "gemini-2.0-") {
					wantOutput = 8192
				}
				if row.MaxOutputTokens != wantOutput {
					t.Errorf("Gemini output limit = %d, want %d", row.MaxOutputTokens, wantOutput)
				}
			case "minimax":
				for _, id := range append([]string{row.ModelID}, row.Aliases...) {
					if !strings.HasPrefix(id, "MiniMax-") {
						t.Errorf("MiniMax model names must retain their exact official case: %q", id)
					}
				}
				if row.MaxInputTokens != 0 || row.MaxOutputTokens != 0 {
					t.Errorf("MiniMax unpublished independent input/output limits must remain unknown: %+v", row.ModelContextCapacity)
				}
			case "qwen":
				if len(row.MatchHosts) == 0 {
					t.Error("Qwen Model Studio capacities require explicit hosted-product restrictions")
				}
				for _, host := range row.MatchHosts {
					if !officialCapacityCatalogTestExactHost(host) || (!strings.HasSuffix(host, ".aliyuncs.com") && !strings.HasSuffix(host, ".alibabacloud.com")) {
						t.Errorf("Qwen host must identify the hosted Alibaba product, not arbitrary self-hosting: %q", host)
					}
				}
			case "kimi":
				for _, id := range append([]string{row.ModelID}, row.Aliases...) {
					if strings.Contains(strings.ToLower(id), "k3[1m]") {
						t.Errorf("catalog must not invent a k3[1m] model identity: %q", id)
					}
					if id != "k3" {
						continue
					}
					foundCodingK3 = true
					if row.Product != "coding" || len(row.MatchAccountModes) != 1 || row.MatchAccountModes[0] != "coding" {
						t.Errorf("k3 alias must remain restricted to Coding accounts: product=%q modes=%v", row.Product, row.MatchAccountModes)
					}
					if row.CapacityBasis != "total_context" || row.ContextWindow != 262144 || row.MaxContextWindow > 262144 {
						t.Errorf("Coding k3 must use its common 262144-token capacity, not the open-platform 1M value: %+v", row.ModelContextCapacity)
					}
				}
			case "doubao":
				if len(row.MatchHosts) == 0 {
					t.Error("Doubao capacities require an explicit cn-beijing product host")
				}
				for _, host := range row.MatchHosts {
					if !officialCapacityCatalogTestExactHost(host) || !strings.Contains(host, "cn-beijing") || !strings.HasSuffix(host, ".volces.com") {
						t.Errorf("Doubao capacity host must identify cn-beijing Ark: %q", host)
					}
				}
				if !strings.Contains(row.NormalizationBasis, "跨官方页面") {
					t.Errorf("Doubao normalization must disclose the cross-official-page inference: %q", row.NormalizationBasis)
				}
			}
		})
	}
	if !foundCodingK3 {
		t.Error("catalog is missing the separately scoped Coding k3 capacity")
	}
}

func TestOfficialModelContextCapacityCatalogVerifiedRepresentativeValues(t *testing.T) {
	t.Parallel()

	rowsByID := make(map[string][]OfficialModelContextCapacity)
	forbiddenIDs := map[string]bool{
		"gpt-5.3-codex-spark": true,
		"deepseek-chat":       true,
		"deepseek-reasoner":   true,
		"qwen-max":            true,
		"qwen-turbo":          true,
		"k3[1m]":              true,
	}
	for _, row := range officialModelContextCapacityCatalog {
		rowsByID[row.ModelID] = append(rowsByID[row.ModelID], row)
		for _, id := range append([]string{row.ModelID}, row.Aliases...) {
			if forbiddenIDs[id] {
				t.Errorf("catalog contains an unverified or retired model identity %q", id)
			}
		}
	}

	for _, want := range []struct {
		modelID                         string
		contextWindow, maxInput, maxOut int64
	}{
		{"gpt-6-astra", 1050000, 0, 128000},
		{"gpt-5.4-mini", 272000, 0, 0},
		{"gpt-4.1", 1047576, 0, 32768},
		{"claude-sonnet-4-5-20250929", 200000, 0, 64000},
		{"deepseek-v4-pro", 1000000, 0, 384000},
		{"qwen3-coder-next", 262144, 204800, 65536},
		{"kimi-k3", 1048576, 0, 0},
		{"glm-5.2", 1000000, 0, 0},
		{"glm-5.1", 204800, 0, 0},
		{"MiniMax-M3", 1000000, 0, 0},
		{"MiniMax-M2.7", 204800, 0, 0},
		{"doubao-seed-evolving", 1048576, 0, 0},
		{"doubao-seed-2-1-pro-260628", 262144, 0, 0},
	} {
		t.Run(want.modelID, func(t *testing.T) {
			rows := rowsByID[want.modelID]
			if len(rows) != 1 {
				t.Fatalf("want one exact verified record, found %d", len(rows))
			}
			got := rows[0]
			planning, ok := modelContextPlanningCapacity(got.ModelContextCapacity)
			if !ok || planning.ContextWindow != want.contextWindow {
				t.Errorf("planning context window = %d, want %d", planning.ContextWindow, want.contextWindow)
			}
			// Zero fixture fields are outside this representative value check;
			// unknown independent limits are covered by the semantic test above.
			if want.maxInput > 0 && got.MaxInputTokens != want.maxInput {
				t.Errorf("maximum input = %d, want %d", got.MaxInputTokens, want.maxInput)
			}
			if want.maxOut > 0 && got.MaxOutputTokens != want.maxOut {
				t.Errorf("maximum output = %d, want %d", got.MaxOutputTokens, want.maxOut)
			}
		})
	}
	for _, modelID := range []string{"glm-5.3", "glm-5.3-flash"} {
		rows := rowsByID[modelID]
		if len(rows) != 1 {
			t.Errorf("%s: want one exact verified record, found %d", modelID, len(rows))
			continue
		}
		if strings.TrimSpace(rows[0].NormalizationBasis) == "" {
			t.Errorf("%s: unit inference must retain its normalization evidence", modelID)
		}
	}
}

func officialCapacityCatalogTestContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func officialCapacityCatalogTestExactHost(host string) bool {
	return host != "" && strings.TrimSpace(host) == host && strings.ToLower(host) == host && !strings.ContainsAny(host, "/*?:@")
}
