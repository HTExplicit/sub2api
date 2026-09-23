package service

import (
	"strings"
	"time"
)

// A refresh rotates credentials for the selected workspace; it must not switch
// the account mapping to whichever workspace the new ID token defaults to.
func preserveOpenAISelectedWorkspace(info *OpenAITokenInfo, account *Account) {
	if info == nil || account == nil {
		return
	}
	selected := strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
	if selected == "" {
		return
	}
	if !strings.EqualFold(selected, strings.TrimSpace(info.ChatGPTAccountID)) {
		// Keep the existing selected-workspace metadata until accounts/check can
		// corroborate it. The other workspace's claim is not evidence about it.
		info.PlanType = account.GetCredential("plan_type")
		info.SubscriptionExpiresAt = account.GetCredential("subscription_expires_at")
		info.OrganizationID = account.GetCredential("organization_id")
		info.planTypeFromOtherWorkspace = true
	}
	info.ChatGPTAccountID = selected
	info.OrganizationID = account.GetCredential("organization_id")
}

func chatGPTAccountInfoForSelectedWorkspace(accounts map[string]any, selected string, now time.Time) *ChatGPTAccountInfo {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return nil
	}
	for key, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok || !strings.EqualFold(chatGPTAccountObjectID(account, key), selected) || !isUsableChatGPTAccountCandidate(account, now) {
			continue
		}
		info := &ChatGPTAccountInfo{}
		fillAccountInfo(info, account, key)
		if strings.TrimSpace(info.PlanType) != "" {
			return info
		}
	}
	return nil
}
