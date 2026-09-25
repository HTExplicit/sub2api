package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

const PromptAccountBindingExtraKey = "prompt_skills"

var ErrPromptRuleReferenced = errors.New("prompt_rule_referenced_by_account")

type BusinessSystemPromptRulesStore interface {
	LoadBusinessSystemPromptRules(context.Context) (BusinessSystemPromptSnapshot, error)
	UpdateBusinessSystemPromptRules(context.Context, extensionv1.PromptRulePolicy, int64, int64) error
}

func (s *BusinessSystemPromptService) SetAccountRepository(repo AccountRepository) {
	s.accountRepo = repo
}

func (s *BusinessSystemPromptService) loadPromptSnapshot(ctx context.Context) (BusinessSystemPromptSnapshot, error) {
	if store, ok := s.store.(BusinessSystemPromptRulesStore); ok {
		return store.LoadBusinessSystemPromptRules(ctx)
	}
	return s.store.LoadBusinessSystemPrompt(ctx)
}

func (s *BusinessSystemPromptService) preparePromptRulesSnapshot(snapshot *BusinessSystemPromptSnapshot) error {
	return s.compilePromptRules(context.Background(), snapshot, nil, false)
}

type PromptRulePolicyState struct {
	Policy         extensionv1.PromptRulePolicy `json:"policy"`
	Revision       int64                        `json:"revision"`
	Enabled        bool                         `json:"enabled"`
	CompactEnabled bool                         `json:"compact_enabled"`
}

func (s *BusinessSystemPromptService) PromptRulesState() (PromptRulePolicyState, error) {
	snapshot, ok := s.CurrentSnapshot()
	if !ok || snapshot.RulePolicy == nil {
		return PromptRulePolicyState{}, ErrBusinessSystemPromptUnavailable
	}
	return PromptRulePolicyState{Policy: *snapshot.RulePolicy, Revision: snapshot.Revision, Enabled: snapshot.Enabled, CompactEnabled: snapshot.CompactEnabled}, nil
}

func (s *BusinessSystemPromptService) UpdatePromptRules(ctx context.Context, policy extensionv1.PromptRulePolicy, expected, actor int64) (PromptRulePolicyState, error) {
	store, ok := s.store.(BusinessSystemPromptRulesStore)
	if !ok {
		return PromptRulePolicyState{}, ErrBusinessSystemPromptUnavailable
	}
	current, ok := s.CurrentSnapshot()
	if !ok {
		return PromptRulePolicyState{}, ErrBusinessSystemPromptUnavailable
	}
	if current.Revision != expected {
		return PromptRulePolicyState{}, ErrBusinessSystemPromptRevisionConflict
	}
	current.RulePolicy = &policy
	if err := s.preparePromptRulesSnapshot(&current); err != nil {
		return PromptRulePolicyState{}, err
	}
	if err := store.UpdateBusinessSystemPromptRules(ctx, *current.RulePolicy, expected, actor); err != nil {
		return PromptRulePolicyState{}, err
	}
	if err := s.Reload(ctx); err != nil {
		return PromptRulePolicyState{}, err
	}
	state, err := s.PromptRulesState()
	if err == nil && s.bus != nil {
		_ = s.bus.Publish(ctx, state.Revision)
	}
	return state, err
}

type PromptAccountBindingView struct {
	AccountID        int64                            `json:"account_id"`
	Name             string                           `json:"name"`
	Platform         string                           `json:"platform"`
	AccountType      string                           `json:"account_type"`
	UpdatedAt        time.Time                        `json:"updated_at"`
	Binding          extensionv1.PromptAccountBinding `json:"binding"`
	EffectiveRuleIDs []string                         `json:"effective_rule_ids"`
	Supported        bool                             `json:"supported"`
}

func promptBindingForAccount(account *Account) (extensionv1.PromptAccountBinding, error) {
	binding := extensionv1.PromptAccountBinding{Mode: "inherit", RuleIDs: []string{}}
	if account == nil || account.Extra == nil || account.Extra[PromptAccountBindingExtraKey] == nil {
		return binding, nil
	}
	raw, err := json.Marshal(account.Extra[PromptAccountBindingExtraKey])
	if err != nil || json.Unmarshal(raw, &binding) != nil {
		return binding, ErrBusinessSystemPromptInvalid
	}
	if binding.Mode != "inherit" && binding.Mode != "off" && binding.Mode != "custom" {
		return binding, ErrBusinessSystemPromptInvalid
	}
	if binding.RuleIDs == nil {
		binding.RuleIDs = []string{}
	}
	return binding, nil
}

func (s *BusinessSystemPromptService) PromptAccountBindings(ctx context.Context, ids []int64) ([]PromptAccountBindingView, error) {
	if s.accountRepo == nil || len(ids) == 0 || len(ids) > 100 {
		return nil, ErrBusinessSystemPromptInvalid
	}
	state, err := s.PromptRulesState()
	if err != nil {
		return nil, err
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(accounts) != len(ids) {
		return nil, ErrAccountNotFound
	}
	views := make([]PromptAccountBindingView, 0, len(accounts))
	for _, account := range accounts {
		binding, err := promptBindingForAccount(account)
		if err != nil {
			return nil, err
		}
		effective := []string{}
		switch binding.Mode {
		case "inherit":
			effective = state.Policy.DefaultRuleIDs
		case "custom":
			effective = binding.RuleIDs
		}
		views = append(views, PromptAccountBindingView{AccountID: account.ID, Name: account.Name, Platform: account.Platform, AccountType: account.Type, UpdatedAt: account.UpdatedAt, Binding: binding, EffectiveRuleIDs: effective, Supported: supportsPromptAccount(account)})
	}
	return views, nil
}

type PromptBindingUpdate struct {
	AccountID         int64                            `json:"account_id"`
	ExpectedUpdatedAt time.Time                        `json:"expected_updated_at"`
	Binding           extensionv1.PromptAccountBinding `json:"binding"`
}

type PromptBindingUpdateResult struct {
	AccountID int64  `json:"account_id"`
	Applied   bool   `json:"applied"`
	Code      string `json:"code,omitempty"`
}

func (s *BusinessSystemPromptService) UpdatePromptAccountBindings(ctx context.Context, updates []PromptBindingUpdate, expectedRevision int64) ([]PromptBindingUpdateResult, error) {
	if len(updates) == 0 || len(updates) > 100 {
		return nil, ErrBusinessSystemPromptInvalid
	}
	writer, ok := s.accountRepo.(interface {
		UpdatePromptBindingIfRevision(context.Context, int64, time.Time, int64, extensionv1.PromptAccountBinding) (bool, error)
	})
	if !ok {
		return nil, ErrBusinessSystemPromptUnavailable
	}
	state, err := s.PromptRulesState()
	if err != nil {
		return nil, err
	}
	if state.Revision != expectedRevision {
		return nil, ErrBusinessSystemPromptRevisionConflict
	}
	ids := []int64{}
	for _, update := range updates {
		if update.AccountID < 1 || update.ExpectedUpdatedAt.IsZero() || slices.Contains(ids, update.AccountID) {
			return nil, ErrBusinessSystemPromptInvalid
		}
		ids = append(ids, update.AccountID)
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(accounts) != len(updates) {
		return nil, ErrAccountNotFound
	}
	for _, update := range updates {
		if update.Binding.Mode != "inherit" && update.Binding.Mode != "off" && update.Binding.Mode != "custom" {
			return nil, ErrBusinessSystemPromptInvalid
		}
		if update.Binding.Mode != "custom" && len(update.Binding.RuleIDs) > 0 {
			return nil, ErrBusinessSystemPromptInvalid
		}
		selected := map[string]bool{}
		for _, id := range update.Binding.RuleIDs {
			if selected[id] || !slices.ContainsFunc(state.Policy.Rules, func(rule extensionv1.PromptRule) bool { return rule.ID == id }) {
				return nil, ErrBusinessSystemPromptInvalid
			}
			selected[id] = true
		}
	}
	results := make([]PromptBindingUpdateResult, 0, len(updates))
	for _, update := range updates {
		index := slices.IndexFunc(accounts, func(account *Account) bool { return account.ID == update.AccountID })
		if index < 0 || !supportsPromptAccount(accounts[index]) {
			results = append(results, PromptBindingUpdateResult{AccountID: update.AccountID, Code: "account_not_supported"})
			continue
		}
		applied, err := writer.UpdatePromptBindingIfRevision(ctx, update.AccountID, update.ExpectedUpdatedAt, expectedRevision, update.Binding)
		result := PromptBindingUpdateResult{AccountID: update.AccountID, Applied: applied}
		if errors.Is(err, ErrBusinessSystemPromptRevisionConflict) {
			result.Code = "system_prompt_revision_conflict"
		} else if err != nil {
			result.Code = "account_update_failed"
		} else if !applied {
			result.Code = "account_revision_conflict"
		}
		results = append(results, result)
	}
	return results, nil
}
