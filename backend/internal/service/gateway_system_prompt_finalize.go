package service

import (
	"bytes"
	"encoding/json"
	"fmt"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const businessSystemPromptBillingUserAgentKey = "business_system_prompt_billing_user_agent"

// FinalizePromptMessageApplication applies the provider's cache and billing
// preparation after placement. The actual emitted structured blocks, including
// cache attributes, become the plan's content and digest. Preview uses the same
// function with a simulated billing user agent.
func FinalizePromptMessageApplication(body []byte, application BusinessSystemPromptApplication, account *Account, settings *SettingService, billingUserAgent string, c *gin.Context) ([]byte, BusinessSystemPromptApplication, error) {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() {
		return body, application, nil
	}
	final := enforceCacheControlLimit(body)
	if settings != nil && settings.IsAnthropicCacheTTL1hInjectionEnabled(promptPolicyRequestContext(c)) {
		final = injectAnthropicCacheControlTTL1h(final)
	}
	if billingUserAgent != "" {
		final = syncBillingHeaderVersion(final, billingUserAgent)
	}
	if bytes.Equal(body, final) || application.RulesPlan == nil || !application.Applied {
		return final, application, nil
	}
	plan := *application.RulesPlan
	plan.Placements = append([]extensionv1.PromptRulePlacement(nil), plan.Placements...)
	application.RulesPlan = &plan
	blocks := gjson.GetBytes(final, "system").Array()
	for index := range plan.Placements {
		placement := &plan.Placements[index]
		if placement.Carrier != "system" || placement.Index == nil {
			continue
		}
		first := *placement.Index
		count := 1
		if placement.ContentFormat == extensionv1.PromptContentAnthropicSystemBlocks {
			count = len(gjson.ParseBytes(placement.StructuredContent).Array())
		}
		if first < 0 || first+count > len(blocks) {
			return nil, application, fmt.Errorf("%w: final structured prompt bounds", ErrBusinessSystemPromptInvalid)
		}
		if placement.ContentFormat == extensionv1.PromptContentAnthropicSystemBlocks {
			emitted := make([]json.RawMessage, 0, count)
			for _, block := range blocks[first : first+count] {
				emitted = append(emitted, json.RawMessage(block.Raw))
			}
			raw, err := json.Marshal(emitted)
			if err != nil {
				return nil, application, err
			}
			placement.Body, placement.StructuredContent = string(raw), raw
		} else {
			placement.Body = blocks[first].Get("text").String()
		}
		hash, _, err := validateBusinessSystemPromptBodyWithLimit(placement.Body, extensionv1.PromptRulesMaxBytes)
		if err != nil {
			return nil, application, err
		}
		placement.SHA256 = hash
	}
	return final, promptpolicy.FinishRulesPlan(application), nil
}
