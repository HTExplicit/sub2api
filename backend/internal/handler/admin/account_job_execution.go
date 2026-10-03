package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (h *AccountHandler) ExecuteAccountJob(
	ctx context.Context,
	job *service.AccountJob,
	payload json.RawMessage,
	items []service.AccountJobItem,
) ([]service.AccountJobExecutionResult, error) {
	if h == nil || job == nil || len(items) != 1 {
		return nil, errors.New("invalid account job execution")
	}
	item := items[0]
	result := h.executeAccountJobItem(ctx, job.Kind, payload, item)
	return []service.AccountJobExecutionResult{result}, nil
}

func (h *AccountHandler) executeAccountJobItem(ctx context.Context, kind string, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	switch kind {
	case service.AccountJobKindBatchDelete:
		id, ok := accountJobTarget(item)
		if !ok {
			return accountJobFailed(item.ID, "target_missing")
		}
		if err := h.adminService.DeleteAccount(ctx, id); err != nil {
			return accountJobFailedWithError(item.ID, "delete_failed", err)
		}
		return accountJobSucceeded(item.ID, map[string]any{"account_id": id})

	case service.AccountJobKindBatchClearError:
		id, ok := accountJobTarget(item)
		if !ok {
			return accountJobFailed(item.ID, "target_missing")
		}
		account, err := h.adminService.ClearAccountError(ctx, id)
		if err != nil {
			return accountJobFailedWithError(item.ID, "clear_error_failed", err)
		}
		if h.tokenCacheInvalidator != nil && account != nil && account.IsOAuth() {
			_ = h.tokenCacheInvalidator.InvalidateToken(ctx, account)
		}
		return accountJobSucceeded(item.ID, map[string]any{"account_id": id})

	case service.AccountJobKindBatchRefresh:
		id, ok := accountJobTarget(item)
		if !ok {
			return accountJobFailed(item.ID, "target_missing")
		}
		account, err := h.adminService.GetAccount(ctx, id)
		if err != nil {
			return accountJobFailedWithError(item.ID, "account_not_found", err)
		}
		_, warning, err := h.refreshSingleAccount(ctx, account)
		if err != nil {
			return accountJobFailedWithError(item.ID, "refresh_failed", err)
		}
		return accountJobSucceeded(item.ID, map[string]any{"account_id": id, "warning": warning})

	case service.AccountJobKindBatchCreate:
		var payload batchCreateJobPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return accountJobFailedWithError(item.ID, "payload_invalid", err)
		}
		if item.Ordinal <= 0 || item.Ordinal > len(payload.Accounts) {
			return accountJobFailedWithError(item.ID, "payload_invalid", accountJobOrdinalError(item.Ordinal, len(payload.Accounts)))
		}
		request := payload.Accounts[item.Ordinal-1]
		created, err := h.createAccountJobAccount(ctx, request)
		if err != nil {
			return withAccountJobMetadata(accountJobFailedWithError(item.ID, "create_failed", err), map[string]any{"name": request.Name})
		}
		h.scheduleOpenAIResponsesProbe(created)
		h.scheduleGrokImportProbe(created)
		return accountJobSucceeded(item.ID, map[string]any{"account_id": created.ID, "name": created.Name})

	case service.AccountJobKindBatchUpdateCredentials:
		var req BatchUpdateCredentialsRequest
		id, ok := accountJobTarget(item)
		if err := json.Unmarshal(raw, &req); err != nil {
			return accountJobFailedWithError(item.ID, "payload_invalid", err)
		}
		if !ok {
			return accountJobFailedWithError(item.ID, "payload_invalid", errAccountJobTargetMissing)
		}
		if _, err := h.adminService.GetAccount(ctx, id); err != nil {
			return accountJobFailedWithError(item.ID, "account_not_found", err)
		}
		updateCredentials := func(mutationCtx context.Context) (*service.Account, error) {
			current, getErr := h.adminService.GetAccount(mutationCtx, id)
			if getErr != nil {
				return nil, getErr
			}
			credentials := cloneAccountJobMap(current.Credentials)
			credentials[req.Field] = req.Value
			return h.adminService.UpdateAccount(mutationCtx, id, &service.UpdateAccountInput{Credentials: credentials})
		}
		if _, err := updateCredentials(ctx); err != nil {
			return accountJobFailedWithError(item.ID, "credentials_update_failed", err)
		}
		return accountJobSucceeded(item.ID, map[string]any{"account_id": id})

	case service.AccountJobKindBulkUpdate:
		return h.executeBulkUpdateJob(ctx, raw, item)

	case service.AccountJobKindBulkTaxonomy:
		return h.executeBulkTaxonomyJob(ctx, raw, item)

	case service.AccountJobKindBatchRefreshTier:
		return h.executeRefreshTierJob(ctx, raw, item)

	case service.AccountJobKindImportData:
		return h.executeDataImportJob(ctx, raw, item)

	case service.AccountJobKindImportCodex:
		return h.executeCodexImportJob(ctx, raw, item)

	default:
		return accountJobFailed(item.ID, "kind_unsupported")
	}
}

func accountJobTarget(item service.AccountJobItem) (int64, bool) {
	returnValue := int64(0)
	if item.TargetAccountID != nil {
		returnValue = *item.TargetAccountID
	}
	return returnValue, returnValue > 0
}

var errAccountJobTargetMissing = errors.New("account job item has no target account")

func accountJobOrdinalError(ordinal, count int) error {
	return fmt.Errorf("account job item ordinal %d is outside the %d submitted entries", ordinal, count)
}

func cloneAccountJobMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (h *AccountHandler) createAccountJobAccount(ctx context.Context, item CreateAccountRequest) (*service.Account, error) {
	if err := service.ValidateOpenAILongContextBillingExtra(item.Platform, item.Extra); err != nil {
		return nil, err
	}
	if err := service.ValidateOpenAIReasoningPolicyExtra(item.Extra); err != nil {
		return nil, err
	}
	if item.RateMultiplier != nil && *item.RateMultiplier < 0 {
		return nil, errors.New("rate_multiplier must be >= 0")
	}
	sanitizeExtraBaseRPM(item.Extra)
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
		Name: item.Name, Notes: item.Notes, Platform: item.Platform, Type: item.Type,
		Credentials: item.Credentials, Extra: item.Extra, ProxyID: item.ProxyID,
		ModelContextOverrides: item.ModelContextOverrides,
		Concurrency:           item.Concurrency, Priority: item.Priority, RateMultiplier: item.RateMultiplier,
		LoadFactor: item.LoadFactor, GroupIDs: item.GroupIDs, ExpiresAt: item.ExpiresAt,
		AutoPauseOnExpired: item.AutoPauseOnExpired, ProbeEnabled: item.ProbeEnabled,
		SkipMixedChannelCheck: item.ConfirmMixedChannelRisk != nil && *item.ConfirmMixedChannelRisk,
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (h *AccountHandler) executeBulkUpdateJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req BulkUpdateAccountsRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	id, ok := accountJobTarget(item)
	if !ok {
		// Legacy filter-only items never persisted an authorized account set.
		// Requiring a new submission is safer than resolving a new set now.
		return accountJobFailed(item.ID, "target_missing")
	}
	req.AccountIDs = []int64{id}
	req.Filters = nil
	succeeded := 0
	failed := 0
	var failures []error
	for _, id := range req.AccountIDs {
		if _, getErr := h.adminService.GetAccount(ctx, id); getErr != nil {
			failed++
			failures = append(failures, getErr)
			continue
		}
		apply := func(mutationCtx context.Context) (*service.Account, error) {
			result, updateErr := h.adminService.BulkUpdateAccounts(mutationCtx, newBulkUpdateAccountInput(req, id))
			if updateErr != nil {
				return nil, updateErr
			}
			if result == nil || result.Success != 1 || result.Failed != 0 {
				return nil, errors.New("bulk account update did not update its target")
			}
			return h.adminService.GetAccount(mutationCtx, id)
		}
		if _, err := apply(ctx); err != nil {
			failed++
			failures = append(failures, err)
			continue
		}
		succeeded++
	}
	if failed > 0 {
		return accountJobFailedWithError(item.ID, "bulk_update_failed", errors.Join(failures...))
	}
	metadata := map[string]any{"success": succeeded, "failed": failed}
	if id, ok := accountJobTarget(item); ok {
		metadata["account_id"] = id
	}
	return accountJobSucceeded(item.ID, metadata)
}

func newBulkUpdateAccountInput(req BulkUpdateAccountsRequest, accountID int64) *service.BulkUpdateAccountsInput {
	return &service.BulkUpdateAccountsInput{
		AccountIDs: []int64{accountID}, Name: req.Name, ProxyID: req.ProxyID,
		Concurrency: req.Concurrency, Priority: req.Priority, RateMultiplier: req.RateMultiplier,
		LoadFactor: req.LoadFactor, Status: req.Status, Schedulable: req.Schedulable,
		GroupIDs: req.GroupIDs, Credentials: cloneAccountJobMap(req.Credentials), Extra: cloneAccountJobMap(req.Extra),
		ProbeEnabled:          req.ProbeEnabled,
		SkipMixedChannelCheck: req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk,
	}
}

func (h *AccountHandler) resolveAccountJobTargetIDs(
	ctx context.Context,
	requested []int64,
	requestFilters *BulkUpdateAccountFilters,
) ([]int64, error) {

	if ids := normalizeInt64IDList(requested); len(ids) > 0 {
		return ids, nil
	}

	filters, err := toServiceBulkUpdateAccountFilters(requestFilters)
	if err != nil {
		return nil, err
	}
	if filters == nil {
		return nil, errors.New("account job target is missing")
	}
	var accounts []service.Account
	if filters.Console != nil {
		console, consoleErr := h.accountConsoleService()
		if consoleErr != nil {
			return nil, consoleErr
		}
		accounts, err = h.listAccountsConsoleFiltered(ctx, console, *filters.Console)
	} else {
		groupID := int64(0)
		switch strings.TrimSpace(filters.Group) {
		case "":
		case accountListGroupUngroupedQueryValue:
			groupID = service.AccountListGroupUngrouped
		default:
			groupID, err = strconv.ParseInt(strings.TrimSpace(filters.Group), 10, 64)
		}
		if err == nil {
			accounts, err = h.listAccountsFiltered(ctx, filters.Platform, filters.Type, filters.Status,
				filters.Search, groupID, filters.PrivacyMode, "id", "asc")
		}
	}
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(accounts))
	for index := range accounts {
		ids = append(ids, accounts[index].ID)
	}
	return normalizeInt64IDList(ids), nil
}

func (h *AccountHandler) executeBulkTaxonomyJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req bulkAccountTaxonomyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	id, ok := accountJobTarget(item)
	if !ok {
		return accountJobFailed(item.ID, "target_missing")
	}
	ids := []int64{id}

	console, err := h.accountTaxonomyMutationService()
	if err != nil {
		return accountJobFailedWithError(item.ID, "taxonomy_unavailable", err)
	}
	updated := 0
	for _, id := range ids {
		if _, getErr := h.adminService.GetAccount(ctx, id); getErr != nil {
			return accountJobFailedWithError(item.ID, "taxonomy_update_failed", getErr)
		}
		apply := func(mutationCtx context.Context) (*service.Account, error) {
			result, updateErr := console.BulkUpdateAccountTaxonomy(mutationCtx, service.BulkAccountTaxonomyInput{
				AccountIDs: []int64{id}, FolderAction: req.FolderAction, FolderID: req.FolderID,
				TagAddIDs: req.TagAddIDs, TagRemoveIDs: req.TagRemoveIDs,
			})
			if updateErr != nil {
				return nil, updateErr
			}
			if result == nil || result.UpdatedCount != 1 {
				return nil, errors.New("taxonomy update did not update its target")
			}
			return h.adminService.GetAccount(mutationCtx, id)
		}
		if _, err := apply(ctx); err != nil {
			return accountJobFailedWithError(item.ID, "taxonomy_update_failed", err)
		}
		updated++
	}
	return accountJobSucceeded(item.ID, map[string]any{"matched_count": len(ids), "updated_count": updated})
}

func (h *AccountHandler) executeRefreshTierJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req BatchRefreshTierRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	var accounts []*service.Account
	if id, ok := accountJobTarget(item); ok {
		account, err := h.adminService.GetAccount(ctx, id)
		if err != nil {
			return accountJobFailedWithError(item.ID, "account_not_found", err)
		}
		accounts = []*service.Account{account}
	} else {
		all, _, err := h.adminService.ListAccounts(ctx, 1, 10000, service.PlatformGemini, service.AccountTypeOAuth, "", "", 0, "", "name", "asc")
		if err != nil {
			return accountJobFailedWithError(item.ID, "account_list_failed", err)
		}
		for index := range all {
			accounts = append(accounts, &all[index])
		}
	}
	updated := 0
	for _, account := range accounts {
		if account == nil || account.Platform != service.PlatformGemini || account.Type != service.AccountTypeOAuth || strings.TrimSpace(account.GetCredential("oauth_type")) != "google_one" {
			continue
		}
		_, extra, credentials, err := h.geminiOAuthService.RefreshAccountGoogleOneTier(ctx, account)
		if err != nil {
			return accountJobFailedWithError(item.ID, "refresh_tier_failed", fmt.Errorf("account %d: %w", account.ID, err))
		}
		if _, err = h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{Credentials: credentials, Extra: extra}); err != nil {
			return accountJobFailedWithError(item.ID, "refresh_tier_update_failed", fmt.Errorf("account %d: %w", account.ID, err))
		}
		updated++
	}
	return accountJobSucceeded(item.ID, map[string]any{"updated_count": updated})
}

func (h *AccountHandler) executeDataImportJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req DataImportRequest
	preparedState, prepared := dataImportJobStateFromContext(ctx)
	if prepared {
		req = preparedState.request
	} else {
		if err := json.Unmarshal(raw, &req); err != nil {
			return accountJobFailedWithError(item.ID, "payload_invalid", err)
		}
		if err := validateDataHeader(req.Data); err != nil {
			return accountJobFailedWithError(item.ID, "payload_invalid", err)
		}
	}
	if item.Ordinal <= 0 || item.Ordinal > len(req.Data.Accounts) {
		return accountJobFailedWithError(item.ID, "payload_invalid", accountJobOrdinalError(item.Ordinal, len(req.Data.Accounts)))
	}
	originalIndex := item.Ordinal - 1
	// Every result names its source entry. Proxy import problems belong to the
	// whole file, so the first entry carries them.
	metadata := map[string]any{"source_index": originalIndex, "name": req.Data.Accounts[originalIndex].Name}
	if prepared && originalIndex == 0 && len(preparedState.proxyErrors) > 0 {
		metadata["proxy_errors"] = preparedState.proxyErrors
	}
	failed := func(code string, err error) service.AccountJobExecutionResult {
		return withAccountJobMetadata(accountJobFailedWithError(item.ID, code, err), metadata)
	}
	var decisions []dataImportDecision
	if prepared {
		decisions = preparedState.decisions
	} else {
		_, currentDecisions, decisionErr := h.previewDataImport(ctx, req)
		if decisionErr != nil {
			return failed(dataImportCodeExecutionFailed, decisionErr)
		}
		decisions = currentDecisions
	}
	if originalIndex >= len(decisions) {
		return failed(dataImportCodeExecutionFailed, fmt.Errorf("data import decision %d is unavailable (%d decisions)", originalIndex, len(decisions)))
	}
	decision := decisions[originalIndex]
	if prepared {
		var decisionErr error
		decision, decisionErr = preparedState.currentDecision(originalIndex)
		if decisionErr != nil {
			return failed(dataImportCodeExecutionFailed, decisionErr)
		}
	}
	if len(decision.MatchedAccountIDs) > 0 {
		metadata["matched_account_ids"] = decision.MatchedAccountIDs
	}
	if decision.rejected() {
		return failed(decision.Code, errors.New(decision.Message))
	}
	if !prepared {
		req.Data.Accounts = []DataAccount{decision.Account}
	}
	importOne := func(mutationCtx context.Context) (*service.Account, DataImportResult, error) {
		var result DataImportResult
		var imported *service.Account
		var importErr error
		if prepared {
			imported, result, importErr = preparedState.executeOne(mutationCtx, originalIndex, decision)
		} else {
			result, importErr = h.importData(mutationCtx, req)
		}
		if importErr != nil || result.AccountFailed > 0 || len(result.Items) != 1 || result.Items[0].AccountID == nil {
			if importErr == nil {
				importErr = dataImportItemResultError(result)
			}
			return nil, result, importErr
		}
		if len(result.Items[0].Warnings) > 0 {
			return imported, result, fmt.Errorf("data import item completed with an incomplete mutation: %s", strings.Join(result.Items[0].Warnings, "; "))
		}
		if imported != nil {
			return imported, result, nil
		}
		updated, getErr := h.adminService.GetAccount(mutationCtx, *result.Items[0].AccountID)
		return updated, result, getErr
	}
	importedAccount, result, err := importOne(ctx)
	if prepared && importedAccount != nil {
		// Even a warning can follow a committed account mutation, so keep the
		// in-memory identity index aligned before reporting the item failure.
		preparedState.recordCommittedAccount(importedAccount)
		h.scheduleGrokImportProbe(importedAccount)
	}
	if !prepared && originalIndex == 0 {
		if proxyErrors := dataImportProxyErrors(result.Errors); len(proxyErrors) > 0 {
			metadata["proxy_errors"] = proxyErrors
		}
	}
	if len(result.Items) == 1 {
		metadata["action"] = result.Items[0].Action
		if result.Items[0].AccountID != nil {
			metadata["account_id"] = *result.Items[0].AccountID
		}
		if len(result.Items[0].Warnings) > 0 {
			metadata["warnings"] = result.Items[0].Warnings
		}
	}
	if err != nil || result.AccountFailed > 0 {
		if err == nil {
			err = dataImportItemResultError(result)
		}
		return failed(dataImportCodeExecutionFailed, err)
	}
	return accountJobSucceeded(item.ID, metadata)
}

// dataImportItemResultError returns the recorded reason of a failed import
// result instead of a generic sentence.
func dataImportItemResultError(result DataImportResult) error {
	if len(result.Items) == 1 && strings.TrimSpace(result.Items[0].Error) != "" {
		return errors.New(result.Items[0].Error)
	}
	messages := make([]string, 0, len(result.Errors))
	for _, item := range result.Errors {
		if item.Kind == "account" && strings.TrimSpace(item.Message) != "" {
			messages = append(messages, item.Message)
		}
	}
	if len(messages) > 0 {
		return errors.New(strings.Join(messages, "; "))
	}
	return fmt.Errorf("data import returned %d item results without an account", len(result.Items))
}

func dataImportProxyErrors(errs []DataImportError) []DataImportError {
	proxies := make([]DataImportError, 0, len(errs))
	for _, item := range errs {
		if item.Kind == "proxy" {
			proxies = append(proxies, item)
		}
	}
	return proxies
}

func (h *AccountHandler) executeCodexImportJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req CodexSessionImportRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	if err := service.ValidateOpenAILongContextBillingExtra(service.PlatformOpenAI, req.Extra); err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	if err := service.ValidateOpenAIReasoningPolicyExtra(req.Extra); err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	entries, err := parseCodexSessionImportEntries(req)
	if err != nil {
		return accountJobFailedWithError(item.ID, "payload_invalid", err)
	}
	if item.Ordinal <= 0 || item.Ordinal > len(entries) {
		return accountJobFailedWithError(item.ID, "payload_invalid", accountJobOrdinalError(item.Ordinal, len(entries)))
	}
	entry := entries[item.Ordinal-1]
	result, err := h.importCodexSessions(ctx, req, []codexImportEntry{entry})
	metadata := map[string]any{"source_index": entry.Index}
	if len(result.Items) == 1 {
		imported := result.Items[0]
		metadata["action"] = imported.Action
		if imported.AccountID > 0 {
			metadata["account_id"] = imported.AccountID
		}
		if imported.Name != "" {
			metadata["name"] = imported.Name
		}
		if imported.Message != "" && imported.Action != "failed" {
			metadata["message"] = imported.Message
		}
	}
	if len(result.Warnings) > 0 {
		warnings := make([]string, 0, len(result.Warnings))
		for _, warning := range result.Warnings {
			warnings = append(warnings, warning.Message)
		}
		metadata["warnings"] = warnings
	}
	if err == nil && result.Failed > 0 {
		err = codexImportResultError(result)
	}
	if err != nil {
		return withAccountJobMetadata(accountJobFailedWithError(item.ID, "import_failed", err), metadata)
	}
	return accountJobSucceeded(item.ID, metadata)
}

// codexImportResultError returns the recorded reason of a failed Codex entry.
func codexImportResultError(result CodexSessionImportResult) error {
	if len(result.Items) == 1 && strings.TrimSpace(result.Items[0].Message) != "" {
		return errors.New(result.Items[0].Message)
	}
	messages := make([]string, 0, len(result.Errors))
	for _, item := range result.Errors {
		if strings.TrimSpace(item.Message) != "" {
			messages = append(messages, item.Message)
		}
	}
	if len(messages) > 0 {
		return errors.New(strings.Join(messages, "; "))
	}
	return fmt.Errorf("codex import reported %d failed entries", result.Failed)
}
