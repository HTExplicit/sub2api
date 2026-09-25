package service

import (
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
)

func observePromptRulesFinal(c *gin.Context, account *Account, protocol string, application BusinessSystemPromptApplication) {
	if application.RulesPlan == nil || account == nil {
		return
	}
	key := businessSystemPromptContextKey(c, "prompt_rules_final_observed", protocol)
	identity := fmt.Sprint(account.ID) + ":" + application.RulesPlan.SHA256 + ":" + protocol
	if value, exists := businessSystemPromptRequestGet(c, key); exists && value == identity {
		return
	}
	businessSystemPromptRequestSet(c, key, identity)
	placements := make([]map[string]any, 0, len(application.RulesPlan.Placements))
	for _, placement := range application.RulesPlan.Placements {
		placements = append(placements, map[string]any{"rule_id": placement.RuleID, "version_id": placement.VersionID, "carrier": placement.Carrier, "role": placement.Role, "position": placement.Position})
	}
	slog.Info("prompt_rules_final", "account_id", account.ID, "protocol", protocol, "revision", application.Revision,
		"applied", application.Applied, "plan_sha256", application.RulesPlan.SHA256, "placements", placements, "skipped", application.RulesPlan.Skipped, "wire_verified", true)
}
