package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type batchTestJobItem struct {
	AccountID       int64  `json:"account_id"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

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
	if err := service.ValidateAccountTestPrompt(p.Prompt); err != nil {
		return nil, nil, err
	}
	invalid := errors.New("invalid or mixed batch test selections")
	if p.hasItems && p.hasLegacy {
		return nil, nil, invalid
	}
	models := make(map[int64]string)
	efforts := make(map[int64]string)
	ids := make([]int64, 0)
	if p.hasItems || p.Items != nil {
		items := make([]batchTestJobItem, 0, len(p.Items))
		for _, item := range p.Items {
			item.ModelID = strings.TrimSpace(item.ModelID)
			if item.AccountID <= 0 || item.ModelID == "" || len(item.ModelID) > 256 || len(item.ReasoningEffort) > 32 || strings.TrimSpace(item.ReasoningEffort) != item.ReasoningEffort {
				return nil, nil, invalid
			}
			if previous, exists := models[item.AccountID]; exists {
				if previous != item.ModelID || efforts[item.AccountID] != item.ReasoningEffort {
					return nil, nil, invalid
				}
				continue
			}
			ids = append(ids, item.AccountID)
			models[item.AccountID] = item.ModelID
			efforts[item.AccountID] = item.ReasoningEffort
			items = append(items, item)
		}
		p.Items = items
	} else {
		p.AccountIDs = normalizeInt64IDList(p.AccountIDs)
		p.ModelID = strings.TrimSpace(p.ModelID)
		if len(p.ModelID) > 256 {
			return nil, nil, invalid
		}
		ids = p.AccountIDs
		for _, id := range ids {
			models[id] = p.ModelID
		}
	}
	if len(ids) == 0 {
		return nil, nil, invalid
	}
	return ids, models, nil
}

type batchTestModelContextKey struct{}

func (h *AccountHandler) BatchTest(c *gin.Context) {
	var req batchTestJobPayload
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "invalid batch test request")
		return
	}
	ids, models, err := req.normalize()
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
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Type      string `json:"type"`
	IsCindy   bool   `json:"is_cindy"`
	Models    any    `json:"models"`
	ErrorCode string `json:"error_code,omitempty"`
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
