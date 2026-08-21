package admin

import (
	"context"
	"encoding/json"
	"errors"
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
			return accountJobFailed(item.ID, "delete_failed")
		}
		return accountJobSucceeded(item.ID, map[string]any{"account_id": id})

	case service.AccountJobKindBatchClearError:
		id, ok := accountJobTarget(item)
		if !ok {
			return accountJobFailed(item.ID, "target_missing")
		}
		account, err := h.adminService.ClearAccountError(ctx, id)
		if err != nil {
			return accountJobFailed(item.ID, "clear_error_failed")
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
			return accountJobFailed(item.ID, "account_not_found")
		}
		_, warning, err := h.refreshSingleAccount(ctx, account)
		if err != nil {
			return accountJobFailed(item.ID, "refresh_failed")
		}
		return accountJobSucceeded(item.ID, map[string]any{"account_id": id, "warning": warning})

	case service.AccountJobKindBatchCreate:
		var payload batchCreateJobPayload
		if json.Unmarshal(raw, &payload) != nil || item.Ordinal <= 0 || item.Ordinal > len(payload.Accounts) {
			return accountJobFailed(item.ID, "payload_invalid")
		}
		request := payload.Accounts[item.Ordinal-1]
		create := func(mutationCtx context.Context) (*service.Account, error) {
			return h.createAccountJobAccount(mutationCtx, request)
		}
		var created *service.Account
		var err error
		if isStrictCindyAccountInput(request.Platform, request.Type, request.Credentials) {
			created, err = h.runCindyAccountJobMutation(ctx, 0, create)
		} else {
			created, err = create(ctx)
		}
		if err != nil {
			return accountJobFailed(item.ID, "create_failed")
		}
		h.scheduleOpenAIResponsesProbe(created)
		h.scheduleGrokImportProbe(created)
		return accountJobSucceeded(item.ID, map[string]any{"account_id": created.ID, "name": created.Name})

	case service.AccountJobKindBatchUpdateCredentials:
		var req BatchUpdateCredentialsRequest
		id, ok := accountJobTarget(item)
		if json.Unmarshal(raw, &req) != nil || !ok {
			return accountJobFailed(item.ID, "payload_invalid")
		}
		account, err := h.adminService.GetAccount(ctx, id)
		if err != nil {
			return accountJobFailed(item.ID, "account_not_found")
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
		if isStrictCindyAccount(account) {
			_, err = h.runCindyAccountJobMutation(ctx, id, updateCredentials)
		} else {
			_, err = updateCredentials(ctx)
		}
		if err != nil {
			return accountJobFailed(item.ID, "credentials_update_failed")
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

	case service.AccountJobKindDuplicateReview:
		var req duplicateReviewRequest
		if json.Unmarshal(raw, &req) != nil {
			return accountJobFailed(item.ID, "payload_invalid")
		}
		metadata, err := h.duplicateReview(ctx, req.AccountIDs)
		if err != nil {
			return accountJobFailed(item.ID, "duplicate_review_failed")
		}
		return accountJobSucceeded(item.ID, metadata)

	case service.AccountJobKindDuplicateMerge:
		var req duplicateMergeRequest
		if json.Unmarshal(raw, &req) != nil {
			return accountJobFailed(item.ID, "payload_invalid")
		}
		metadata, err := h.mergeDuplicateAccounts(ctx, req)
		if err != nil {
			return accountJobFailed(item.ID, "duplicate_merge_failed")
		}
		return accountJobSucceeded(item.ID, metadata)

	case service.AccountJobKindCindyConfirmedCleanup:
		var req struct {
			ExpectedCount int    `json:"expected_count"`
			Fingerprint   string `json:"fingerprint"`
		}
		if json.Unmarshal(raw, &req) != nil {
			return accountJobFailed(item.ID, "payload_invalid")
		}
		result, err := h.adminService.DeleteCindyInsufficient(ctx, req.ExpectedCount, req.Fingerprint)
		if err != nil {
			return accountJobFailed(item.ID, "cleanup_failed")
		}
		return accountJobSucceeded(item.ID, map[string]any{"deleted_count": result.DeletedCount})
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

func cloneAccountJobMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (h *AccountHandler) createAccountJobAccount(ctx context.Context, item CreateAccountRequest) (*service.Account, error) {
	if err := validateCreateAccountProviderIdentity(item); err != nil {
		return nil, err
	}
	if err := service.ValidateOpenAILongContextBillingExtra(item.Platform, item.Extra); err != nil {
		return nil, err
	}
	if item.RateMultiplier != nil && *item.RateMultiplier < 0 {
		return nil, errors.New("rate_multiplier must be >= 0")
	}
	sanitizeExtraBaseRPM(item.Extra)
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
		Name: item.Name, Notes: item.Notes, Platform: item.Platform, Type: item.Type,
		Credentials: item.Credentials, Extra: item.Extra, ProxyID: item.ProxyID,
		Concurrency: item.Concurrency, Priority: item.Priority, RateMultiplier: item.RateMultiplier,
		LoadFactor: item.LoadFactor, GroupIDs: item.GroupIDs, ExpiresAt: item.ExpiresAt,
		AutoPauseOnExpired: item.AutoPauseOnExpired, ProbeEnabled: item.ProbeEnabled,
		SkipMixedChannelCheck: item.ConfirmMixedChannelRisk != nil && *item.ConfirmMixedChannelRisk,
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

func isStrictCindyAccountInput(platform, accountType string, credentials map[string]any) bool {
	resolvedPlatform, wirePlatform, profile, err := service.ResolveAccountProviderIdentity(platform, accountType, credentials)
	return err == nil && resolvedPlatform == service.PlatformCindy &&
		wirePlatform == service.WirePlatformOpenAI && profile == service.ProviderProfileCindyLaxaV1
}

func isStrictCindyAccount(account *service.Account) bool {
	return account != nil && account.Platform == service.PlatformCindy &&
		account.EffectiveWirePlatform() == service.WirePlatformOpenAI &&
		account.EffectiveProviderProfile() == service.ProviderProfileCindyLaxaV1 &&
		service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}

func (h *AccountHandler) runCindyAccountJobMutation(
	ctx context.Context,
	accountID int64,
	mutate func(context.Context) (*service.Account, error),
) (*service.Account, error) {
	if h == nil || h.cindyJobMutations == nil {
		return nil, errors.New("Cindy account job mutation is unavailable")
	}
	return h.cindyJobMutations.Run(ctx, accountID, mutate)
}

func (h *AccountHandler) executeBulkUpdateJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req BulkUpdateAccountsRequest
	if json.Unmarshal(raw, &req) != nil {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	ids, err := h.resolveAccountJobTargetIDs(ctx, req.AccountIDs, req.Filters)
	if err != nil {
		return accountJobFailed(item.ID, "filters_invalid")
	}
	if id, ok := accountJobTarget(item); ok {
		ids = []int64{id}
	}
	succeeded := 0
	failed := 0
	for _, id := range ids {
		account, getErr := h.adminService.GetAccount(ctx, id)
		if getErr != nil {
			failed++
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
		if isStrictCindyAccount(account) {
			_, getErr = h.runCindyAccountJobMutation(ctx, id, apply)
		} else {
			_, getErr = apply(ctx)
		}
		if getErr != nil {
			failed++
			continue
		}
		succeeded++
	}
	if failed > 0 {
		return accountJobFailed(item.ID, "bulk_update_failed")
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
	if json.Unmarshal(raw, &req) != nil {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	ids, err := h.resolveAccountJobTargetIDs(ctx, req.AccountIDs, req.Filters)
	if err != nil {
		return accountJobFailed(item.ID, "filters_invalid")
	}
	if id, ok := accountJobTarget(item); ok {
		ids = []int64{id}
	} else if req.Filters != nil && (req.ExpectedMatchCount == nil || *req.ExpectedMatchCount != len(ids)) {
		return accountJobFailed(item.ID, "taxonomy_target_changed")
	}
	console, err := h.accountTaxonomyMutationService()
	if err != nil {
		return accountJobFailed(item.ID, "taxonomy_unavailable")
	}
	updated := 0
	for _, id := range ids {
		account, getErr := h.adminService.GetAccount(ctx, id)
		if getErr != nil {
			return accountJobFailed(item.ID, "taxonomy_update_failed")
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
		if isStrictCindyAccount(account) {
			_, getErr = h.runCindyAccountJobMutation(ctx, id, apply)
		} else {
			_, getErr = apply(ctx)
		}
		if getErr != nil {
			return accountJobFailed(item.ID, "taxonomy_update_failed")
		}
		updated++
	}
	return accountJobSucceeded(item.ID, map[string]any{"matched_count": len(ids), "updated_count": updated})
}

func (h *AccountHandler) executeRefreshTierJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req BatchRefreshTierRequest
	if json.Unmarshal(raw, &req) != nil {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	var accounts []*service.Account
	if id, ok := accountJobTarget(item); ok {
		account, err := h.adminService.GetAccount(ctx, id)
		if err != nil {
			return accountJobFailed(item.ID, "account_not_found")
		}
		accounts = []*service.Account{account}
	} else {
		all, _, err := h.adminService.ListAccounts(ctx, 1, 10000, service.PlatformGemini, service.AccountTypeOAuth, "", "", 0, "", "name", "asc")
		if err != nil {
			return accountJobFailed(item.ID, "account_list_failed")
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
			return accountJobFailed(item.ID, "refresh_tier_failed")
		}
		if _, err = h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{Credentials: credentials, Extra: extra}); err != nil {
			return accountJobFailed(item.ID, "refresh_tier_update_failed")
		}
		updated++
	}
	return accountJobSucceeded(item.ID, map[string]any{"updated_count": updated})
}

func (h *AccountHandler) executeDataImportJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req DataImportRequest
	if json.Unmarshal(raw, &req) != nil || validateDataHeader(req.Data) != nil || item.Ordinal <= 0 || item.Ordinal > len(req.Data.Accounts) {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	originalIndex := item.Ordinal - 1
	account := req.Data.Accounts[originalIndex]
	enrichCredentialsFromIDToken(&account)
	if err := validateDataAccountV2(account); err != nil {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	req.Data.Accounts = []DataAccount{account}
	importOne := func(mutationCtx context.Context) (*service.Account, DataImportResult, error) {
		result, importErr := h.importData(mutationCtx, req)
		if importErr != nil || result.AccountFailed > 0 || len(result.Items) != 1 || result.Items[0].AccountID == nil {
			if importErr == nil {
				importErr = errors.New("data import item failed")
			}
			return nil, result, importErr
		}
		if len(result.Items[0].Warnings) > 0 {
			return nil, result, errors.New("data import item completed with an incomplete mutation")
		}
		updated, getErr := h.adminService.GetAccount(mutationCtx, *result.Items[0].AccountID)
		return updated, result, getErr
	}
	var result DataImportResult
	var err error
	if isStrictCindyAccountInput(account.Platform, account.Type, account.Credentials) {
		targetID, targetErr := h.resolveDataImportCindyTarget(ctx, account)
		if targetErr != nil {
			return accountJobFailed(item.ID, "import_failed")
		}
		_, err = h.runCindyAccountJobMutation(ctx, targetID, func(mutationCtx context.Context) (*service.Account, error) {
			var imported *service.Account
			imported, result, targetErr = importOne(mutationCtx)
			return imported, targetErr
		})
	} else {
		_, result, err = importOne(ctx)
	}
	if err != nil || result.AccountFailed > 0 {
		return accountJobFailed(item.ID, "import_failed")
	}
	metadata := map[string]any{"source_index": originalIndex}
	if len(result.Items) == 1 {
		metadata["action"] = result.Items[0].Action
		if result.Items[0].AccountID != nil {
			metadata["account_id"] = *result.Items[0].AccountID
		}
	}
	return accountJobSucceeded(item.ID, metadata)
}

func (h *AccountHandler) resolveDataImportCindyTarget(ctx context.Context, item DataAccount) (int64, error) {
	accounts, err := h.listAccountsFiltered(ctx, service.PlatformCindy, service.AccountTypeAPIKey, "", "", 0, "", "id", "asc")
	if err != nil {
		return 0, err
	}
	matches := buildDataIdentityIndex(accounts).Find(dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra))
	if len(matches) > 1 {
		return 0, errors.New("multiple current Cindy credential identity matches")
	}
	if len(matches) == 1 {
		return matches[0].AccountID, nil
	}
	return 0, nil
}

func (h *AccountHandler) executeCodexImportJob(ctx context.Context, raw json.RawMessage, item service.AccountJobItem) service.AccountJobExecutionResult {
	var req CodexSessionImportRequest
	if json.Unmarshal(raw, &req) != nil {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	if err := service.ValidateOpenAILongContextBillingExtra(service.PlatformOpenAI, req.Extra); err != nil {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	entries, err := parseCodexSessionImportEntries(req)
	if err != nil || item.Ordinal <= 0 || item.Ordinal > len(entries) {
		return accountJobFailed(item.ID, "payload_invalid")
	}
	result, err := h.importCodexSessions(ctx, req, []codexImportEntry{entries[item.Ordinal-1]})
	if err != nil || result.Failed > 0 {
		return accountJobFailed(item.ID, "import_failed")
	}
	metadata := map[string]any{"source_index": entries[item.Ordinal-1].Index}
	if len(result.Items) == 1 {
		metadata["action"] = result.Items[0].Action
		if result.Items[0].AccountID > 0 {
			metadata["account_id"] = result.Items[0].AccountID
		}
	}
	return accountJobSucceeded(item.ID, metadata)
}
