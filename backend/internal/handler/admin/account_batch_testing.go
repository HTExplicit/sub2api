package admin

import (
	"context"
	"encoding/json"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type batchTestJobItem = extensionv1.BatchTestSelection

type batchTestJobPayload struct {
	Prompt     string             `json:"prompt,omitempty"`
	AccountIDs []int64            `json:"account_ids,omitempty"`
	ModelID    string             `json:"model_id,omitempty"`
	Items      []batchTestJobItem `json:"items,omitempty"`
	hasItems   bool
	hasLegacy  bool
}

func (p *batchTestJobPayload) UnmarshalJSON(raw []byte) error {
	type wire batchTestJobPayload
	var value wire
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*p = batchTestJobPayload(value)
	_, p.hasItems = fields["items"]
	_, ids := fields["account_ids"]
	_, model := fields["model_id"]
	p.hasLegacy = ids || model
	return nil
}

// Normalize once before persistence and again on recovery of an older payload.
// Models are keyed by account identity, never by task ordinals (which change on retry).
func (p *batchTestJobPayload) normalize() ([]int64, map[int64]string, error) {
	return p.normalizeContext(context.Background())
}
func (p *batchTestJobPayload) normalizeContext(ctx context.Context) ([]int64, map[int64]string, error) {
	if err := service.ValidateAccountTestPrompt(p.Prompt); err != nil {
		return nil, nil, err
	}
	explicit := p.hasItems || p.Items != nil
	plan, err := service.PlanBatchAccountTests(ctx, extensionv1.BatchTestPlanningRequest{HasItems: explicit, HasLegacy: p.hasLegacy, AccountIDs: p.AccountIDs, ModelID: p.ModelID, Items: p.Items})
	if err != nil {
		return nil, nil, err
	}
	if explicit {
		p.Items = plan.Items
	} else {
		p.AccountIDs, p.ModelID = plan.AccountIDs, plan.ModelID
	}
	return plan.AccountIDs, plan.Models, nil
}

type batchTestModelContextKey struct{}

func (h *AccountHandler) BatchTest(c *gin.Context) {
	var req batchTestJobPayload
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "invalid batch test request")
		return
	}
	ids, models, err := req.normalizeContext(c.Request.Context())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	seeds := accountJobSeeds(ids)
	efforts := make(map[int64]string, len(req.Items))
	for _, item := range req.Items {
		efforts[item.AccountID] = item.ReasoningEffort
	}
	for i, id := range ids {
		metadata := map[string]any{"model_id": models[id]}
		if effort := efforts[id]; effort != "" {
			metadata["reasoning_effort"] = effort
		}
		seeds[i].Metadata, _ = json.Marshal(metadata)
	}
	h.submitAccountJob(c, service.AccountJobKindBatchTest, req, seeds)
}

// Shared across requests, including catalog loads by multiple administrators.
var batchTestCatalogSlots = make(chan struct{}, 5)

type batchTestModelRow struct {
	AccountID int64                `json:"account_id"`
	Name      string               `json:"name"`
	Platform  string               `json:"platform"`
	Type      string               `json:"type"`
	IsCindy   bool                 `json:"is_cindy"`
	Models    any                  `json:"models"`
	ErrorCode string               `json:"error_code,omitempty"`
	TestPlan  *accountTestPlanView `json:"test_plan,omitempty"`
}

func (h *AccountHandler) BatchTestModels(c *gin.Context) {
	var req accountIDsJobPayload
	if c.ShouldBindJSON(&req) != nil || len(req.AccountIDs) == 0 || len(req.AccountIDs) > 100 {
		response.BadRequest(c, "provide between 1 and 100 account_ids")
		return
	}
	for _, id := range req.AccountIDs {
		if id <= 0 {
			response.BadRequest(c, "invalid account_id")
			return
		}
	}
	ids := normalizeInt64IDList(req.AccountIDs)
	withPlan, err := accountTestPlanRequested(c.Query("view"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if _, err := service.PlanBatchAccountTests(c.Request.Context(), extensionv1.BatchTestPlanningRequest{HasLegacy: true, AccountIDs: ids}); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	byID := make(map[int64]*service.Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	rows := make([]batchTestModelRow, len(ids))
	var wg sync.WaitGroup
	ctx := c.Request.Context()
	for i, id := range ids {
		rows[i] = batchTestModelRow{AccountID: id, Models: []any{}}
		account := byID[id]
		if account == nil {
			rows[i].ErrorCode = "account_not_found"
			continue
		}
		wg.Add(1)
		go func(i int, account *service.Account) {
			defer wg.Done()
			select {
			case batchTestCatalogSlots <- struct{}{}:
				defer func() { <-batchTestCatalogSlots }()
			case <-ctx.Done():
				rows[i].ErrorCode = "catalog_canceled"
				return
			}
			rows[i].Name, rows[i].Platform, rows[i].Type = account.Name, account.Platform, account.Type
			rows[i].IsCindy = service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
			if withPlan {
				plan, err := h.accountTestPlan(ctx, account)
				if err != nil || ctx.Err() != nil {
					rows[i].ErrorCode = "catalog_failed"
					return
				}
				rows[i].Models, rows[i].TestPlan = plan.Models, plan
				return
			}
			models, err := h.accountTestModels(ctx, account)
			if err != nil || ctx.Err() != nil {
				rows[i].ErrorCode = "catalog_failed"
				return
			}
			rows[i].Models = models
		}(i, account)
	}
	wg.Wait()
	response.Success(c, gin.H{"items": rows})
}
