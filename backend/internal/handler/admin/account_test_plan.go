package admin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	// Official providers do not acquire an account-tools dependency merely to
	// display a model list or perform a basic connection test.
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
	connection := accountTestModeView{ModelIDs: []string{}}
	for _, model := range models {
		id, _ := model["id"].(string)
		if accountConnectionModel(account, model) {
			connection.ModelIDs = append(connection.ModelIDs, id)
		}
	}
	if slices.Contains(connection.ModelIDs, defaultID) {
		connection.DefaultModelID = defaultID
	} else if len(connection.ModelIDs) > 0 {
		connection.DefaultModelID = connection.ModelIDs[0]
	}
	return &accountTestPlanView{SchemaVersion: 1, AccountID: account.ID, WirePlatform: account.Platform,
		DefaultMode: "default", Models: models, ModeViews: map[string]accountTestModeView{
			"default": view, "compact": view, "text": view, "connection": connection,
		}}
}

// A connection probe needs text conversation. Explicit capability declarations
// take precedence; catalog entries without them use the same model identities
// and account mappings used by the test dispatcher.
func accountConnectionModel(account *service.Account, model map[string]any) bool {
	id, _ := model["id"].(string)
	if kind, _ := model["kind"].(string); kind != "" {
		return kind == "text"
	}
	declaredText := false
	for _, key := range []string{"output_modalities", "input_modalities"} {
		if values, ok := model[key].([]any); ok && len(values) > 0 {
			text := false
			for _, value := range values {
				text = text || value == "text"
			}
			if !text {
				return false
			}
			if key == "output_modalities" {
				declaredText = true
			}
		}
	}
	if declaredText {
		return true
	}
	var catalogTargets []string
	if target, ok := model["live_upstream_id"].(string); ok {
		catalogTargets = []string{target}
	}
	if !service.AccountTestSupportsTextConversation(account, id, catalogTargets...) {
		return false
	}
	if endpoints, ok := model["endpoints"].([]any); ok && len(endpoints) > 0 {
		for _, value := range endpoints {
			endpoint, _ := value.(string)
			lower := strings.ToLower(endpoint)
			if strings.Contains(lower, "responses") || strings.Contains(lower, "chat") || strings.Contains(lower, "messages") || strings.Contains(lower, "generatecontent") {
				return true
			}
		}
		return false
	}
	return true
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
	defaultID := ""
	if len(models) > 0 {
		defaultID, _ = models[0]["id"].(string)
		if account.Platform != service.PlatformGemini {
			for _, model := range models {
				if id, _ := model["id"].(string); strings.Contains(id, "sonnet") {
					defaultID = id
					break
				}
			}
		}
	}
	plan := basicAccountTestPlan(account, models, defaultID)
	if account.Platform != service.PlatformGrok {
		return plan, nil
	}
	plan.DefaultMode = "text"
	plan.ModeViews = map[string]accountTestModeView{"connection": plan.ModeViews["connection"]}
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
				for _, id := range view.ModelIDs {
					if id == "grok" {
						view.DefaultModelID = id
						break
					}
				}
				for _, id := range view.ModelIDs {
					if strings.Contains(id, "grok-4.5") {
						view.DefaultModelID = id
						break
					}
				}
			}
		}
		plan.ModeViews[mode] = view
	}
	plan.ModeViews["default"] = plan.ModeViews["text"]
	if slices.Contains(plan.ModeViews["connection"].ModelIDs, plan.ModeViews["text"].DefaultModelID) {
		connection := plan.ModeViews["connection"]
		connection.DefaultModelID = plan.ModeViews["text"].DefaultModelID
		plan.ModeViews["connection"] = connection
	}
	return plan, nil
}
