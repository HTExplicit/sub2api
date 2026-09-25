package admin

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const accountTestPlanViewName = "account-test-plan-v1"

type accountTestModeView struct {
	ModelIDs       []string `json:"model_ids"`
	DefaultModelID string   `json:"default_model_id"`
}

type accountTestPlanView struct {
	SchemaVersion int                            `json:"schema_version"`
	AccountID     int64                          `json:"account_id"`
	WirePlatform  string                         `json:"wire_platform"`
	DefaultMode   string                         `json:"default_mode"`
	Models        []map[string]any               `json:"models"`
	ModeViews     map[string]accountTestModeView `json:"mode_views"`
	PolicyStamp   string                         `json:"policy_stamp,omitempty"`
}

func accountTestPlanRequested(view string) (bool, error) {
	switch view {
	case "":
		return false, nil
	case accountTestPlanViewName:
		return true, nil
	default:
		return false, errors.New("unsupported account test model view")
	}
}

func (h *AccountHandler) accountTestPlan(ctx context.Context, account *service.Account) (*accountTestPlanView, error) {
	if account == nil || account.ID <= 0 {
		return nil, errors.New("account test target is unavailable")
	}
	if service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		provider, err := service.LoadCindyAccountTestPlan(ctx, account)
		if err != nil {
			return nil, err
		}
		models, err := accountTestPlanModels(provider.Models)
		if err != nil {
			return nil, err
		}
		plan := basicAccountTestPlan(account, models, provider.DefaultModelID)
		plan.PolicyStamp = provider.Namespace
		return plan, nil
	}
	// Official providers do not acquire an account-tools or Cindy dependency
	// merely to display a model list or perform a basic connection test.
	raw, err := h.accountTestModels(ctx, account)
	if err != nil {
		return nil, err
	}
	return ordinaryAccountTestPlan(account, raw)
}

func accountTestPlanModels(raw any) ([]map[string]any, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var models []map[string]any
	if json.Unmarshal(encoded, &models) != nil {
		return nil, errors.New("invalid account test model catalog")
	}
	if models == nil {
		models = []map[string]any{}
	}
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		id, ok := model["id"].(string)
		if !ok || strings.TrimSpace(id) == "" || seen[id] {
			return nil, errors.New("invalid account test model identity")
		}
		seen[id] = true
		if name, ok := model["display_name"].(string); !ok || strings.TrimSpace(name) == "" {
			model["display_name"] = id
		}
		if _, ok := model["type"].(string); !ok {
			model["type"] = "model"
		}
	}
	return models, nil
}

func basicAccountTestPlan(account *service.Account, models []map[string]any, defaultID string) *accountTestPlanView {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		// Both callers validate these freshly decoded maps with accountTestPlanModels.
		id, _ := model["id"].(string)
		ids = append(ids, id)
	}
	view := accountTestModeView{ModelIDs: ids, DefaultModelID: defaultID}
	return &accountTestPlanView{SchemaVersion: 1, AccountID: account.ID, WirePlatform: account.EffectiveWirePlatform(),
		DefaultMode: "default", Models: models, ModeViews: map[string]accountTestModeView{
			"default": view, "compact": view, "text": view,
		}}
}

// connectionTestDefault applies the batch test's automatic model choice to a
// displayed list, falling back to its first entry for media-only accounts.
func connectionTestDefault(account *service.Account, ids []string) string {
	if id := service.PickConnectionTestModel(account, ids...); id != "" {
		return id
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// These choices originate in the official single-account test UI. Keep their
// current behavior in one core view producer, not in two frontend bundles.
func ordinaryAccountTestPlan(account *service.Account, raw any) (*accountTestPlanView, error) {
	models, err := accountTestPlanModels(raw)
	if err != nil {
		return nil, err
	}
	if account.Platform == service.PlatformGemini || account.Platform == service.PlatformAntigravity {
		priorities := map[string]int{}
		for index, id := range []string{"gemini-3.1-flash-image", "gemini-2.5-flash-image", "gemini-3.5-flash", "gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-2.0-flash"} {
			priorities[id] = index
		}
		priority := func(model map[string]any) int {
			id, _ := model["id"].(string) // Validated above before sorting.
			if value, ok := priorities[id]; ok {
				return value
			}
			return len(priorities)
		}
		sort.SliceStable(models, func(i, j int) bool { return priority(models[i]) < priority(models[j]) })
	}
	ids := make([]string, 0, len(models))
	for _, model := range models {
		id, _ := model["id"].(string) // Validated above.
		ids = append(ids, id)
	}
	plan := basicAccountTestPlan(account, models, connectionTestDefault(account, ids))
	if account.Platform != service.PlatformGrok {
		return plan, nil
	}
	plan.DefaultMode = "text"
	plan.ModeViews = map[string]accountTestModeView{}
	for _, mode := range []string{"text", "image", "video", "search", "tts", "stt", "realtime"} {
		view := accountTestModeView{ModelIDs: []string{}}
		for _, model := range models {
			id, _ := model["id"].(string)
			lower := strings.ToLower(id)
			image := lower == "grok-imagine" || lower == "grok-imagine-edit" || strings.HasPrefix(lower, "grok-imagine-image")
			video := strings.HasPrefix(lower, "grok-imagine-video") || strings.HasPrefix(lower, "grok-video")
			if (mode == "text" && !image && !video) || (mode == "image" && image) || (mode == "video" && video) {
				view.ModelIDs = append(view.ModelIDs, id)
			}
		}
		if len(view.ModelIDs) > 0 {
			view.DefaultModelID = view.ModelIDs[0]
			if mode == "text" {
				view.DefaultModelID = connectionTestDefault(account, view.ModelIDs)
			}
		}
		plan.ModeViews[mode] = view
	}
	plan.ModeViews["default"] = plan.ModeViews["text"]
	return plan, nil
}
