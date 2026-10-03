package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type dataImportPreviewStateContextKey struct{}
type dataImportJobStateContextKey struct{}
type dataImportReferenceCacheContextKey struct{}

type dataImportPreviewState struct {
	identityIndex *dataIdentityIndex
}

type dataImportReferenceCache struct {
	groupIDs map[int64]struct{}
	proxyIDs map[int64]struct{}
}

func dataImportReferenceCacheFromContext(ctx context.Context) (*dataImportReferenceCache, bool) {
	if ctx == nil {
		return nil, false
	}
	cache, ok := ctx.Value(dataImportReferenceCacheContextKey{}).(*dataImportReferenceCache)
	return cache, ok && cache != nil
}

func dataImportPreviewStateFromContext(ctx context.Context) (*dataImportPreviewState, bool) {
	if ctx == nil {
		return nil, false
	}
	state, ok := ctx.Value(dataImportPreviewStateContextKey{}).(*dataImportPreviewState)
	return state, ok && state != nil
}

type dataImportJobState struct {
	handler              *AccountHandler
	request              DataImportRequest
	decisions            []dataImportDecision
	proxyKeyToID         map[string]int64
	groups               *dataImportGroupResolver
	taxonomy             *dataImportTaxonomyResolver
	skipDefaultGroupBind bool
	identityIndex        *dataIdentityIndex
	// proxyErrors keeps what went wrong while importing the file's proxies.
	proxyErrors []DataImportError
}

func dataImportJobStateFromContext(ctx context.Context) (*dataImportJobState, bool) {
	if ctx == nil {
		return nil, false
	}
	state, ok := ctx.Value(dataImportJobStateContextKey{}).(*dataImportJobState)
	return state, ok && state != nil
}

func (h *AccountHandler) PrepareAccountJob(
	ctx context.Context,
	job *service.AccountJob,
	raw json.RawMessage,
) (context.Context, func(), error) {
	if h == nil || job == nil || job.Kind != service.AccountJobKindImportData {
		return ctx, func() {}, nil
	}
	var req DataImportRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return ctx, nil, fmt.Errorf("invalid data import job payload: %w", err)
	}
	if err := validateDataHeader(req.Data); err != nil {
		return ctx, nil, fmt.Errorf("invalid data import job payload: %w", err)
	}
	if err := validateDataImportRequest(req); err != nil {
		return ctx, nil, err
	}

	existing, err := h.listAccountsFiltered(ctx, "", "", "", "", 0, "", "id", "asc")
	if err != nil {
		return ctx, nil, err
	}
	identityIndex := buildDataIdentityIndex(existing)
	var allGroups []service.Group
	needsGroups := dataImportNeedsGroupNames(req.Data.Accounts) ||
		(req.UniformSettings.GroupIDs != nil && len(*req.UniformSettings.GroupIDs) > 0)
	if needsGroups {
		allGroups, err = h.adminService.GetAllGroupsIncludingInactive(ctx)
		if err != nil {
			return ctx, nil, err
		}
	}
	previewState := &dataImportPreviewState{identityIndex: identityIndex}
	decisionCtx := context.WithValue(ctx, dataImportPreviewStateContextKey{}, previewState)
	_, decisions, err := h.previewDataImport(decisionCtx, req)
	if err != nil {
		return ctx, nil, err
	}

	proxyResult := DataImportResult{AccountIDs: []int64{}, Items: []DataImportItemResult{}}
	proxyKeyToID, err := h.importDataProxies(ctx, req.Data.Proxies, &proxyResult)
	if err != nil {
		return ctx, nil, err
	}

	var taxonomy *dataImportTaxonomyResolver
	if dataImportNeedsTaxonomy(req) {
		console, consoleErr := h.accountConsoleService()
		if consoleErr != nil {
			return ctx, nil, consoleErr
		}
		taxonomy, err = newDataImportTaxonomyResolver(ctx, console)
		if err != nil {
			return ctx, nil, err
		}
	}

	var groups *dataImportGroupResolver
	if dataImportNeedsGroupNames(req.Data.Accounts) {
		groups = newDataImportGroupResolver(allGroups)
	}
	groupIDs := make(map[int64]struct{}, len(allGroups))
	for _, group := range allGroups {
		groupIDs[group.ID] = struct{}{}
	}
	proxyIDs := make(map[int64]struct{}, len(proxyKeyToID))
	for _, proxyID := range proxyKeyToID {
		if proxyID > 0 {
			proxyIDs[proxyID] = struct{}{}
		}
	}
	referenceCache := &dataImportReferenceCache{groupIDs: groupIDs, proxyIDs: proxyIDs}

	skipDefaultGroupBind := true
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}
	state := &dataImportJobState{
		handler:              h,
		request:              req,
		decisions:            decisions,
		proxyKeyToID:         proxyKeyToID,
		groups:               groups,
		taxonomy:             taxonomy,
		skipDefaultGroupBind: skipDefaultGroupBind,
		identityIndex:        identityIndex,
		proxyErrors:          dataImportProxyErrors(proxyResult.Errors),
	}
	preparedCtx := context.WithValue(ctx, dataImportReferenceCacheContextKey{}, referenceCache)
	preparedCtx = context.WithValue(preparedCtx, dataImportJobStateContextKey{}, state)
	cleanup := func() {
		state.request = DataImportRequest{}
		state.decisions = nil
		state.proxyKeyToID = nil
		state.identityIndex = nil
		state.groups = nil
		state.taxonomy = nil
		state.proxyErrors = nil
		referenceCache.groupIDs = nil
		referenceCache.proxyIDs = nil
	}
	return preparedCtx, cleanup, nil
}

func (s *dataImportJobState) currentDecision(index int) (dataImportDecision, error) {
	if s == nil || index < 0 || index >= len(s.decisions) {
		return dataImportDecision{}, errors.New("data import decision is unavailable")
	}
	decision := s.decisions[index]
	if decision.rejected() {
		return decision, nil
	}
	matches := s.identityIndex.Find(dataAccountIdentityKeys(
		decision.Account.Platform, decision.Account.Credentials, decision.Account.Extra,
	))
	decision.MatchedAccountIDs = nil
	for _, match := range matches {
		decision.MatchedAccountIDs = append(decision.MatchedAccountIDs, match.AccountID)
	}
	switch len(matches) {
	case 0:
		decision.Action = dataImportActionCreate
		decision.Code = dataImportCodeCreate
		decision.AccountID = nil
	case 1:
		accountID := matches[0].AccountID
		decision.Action = dataImportActionUpdate
		decision.Code = dataImportCodeUpdate
		decision.AccountID = &accountID
	default:
		rejectDataImportDecision(&decision, dataImportCodeIdentityConflict)
		return decision, nil
	}
	decision.Message = dataImportDecisionMessage(decision)
	return decision, nil
}

func (s *dataImportJobState) executeOne(
	ctx context.Context,
	index int,
	decision dataImportDecision,
) (*service.Account, DataImportResult, error) {
	result := DataImportResult{AccountIDs: []int64{}, Items: []DataImportItemResult{}}
	if s == nil || s.handler == nil || index < 0 || index >= len(s.decisions) {
		return nil, result, errors.New("data import decision is unavailable")
	}
	itemResult := DataImportItemResult{Index: index, Name: decision.Account.Name, Action: decision.Action}
	if decision.rejected() {
		itemResult.Code = decision.Code
		itemResult.Message = decision.Message
		itemResult.Error = decision.Message
		result.AccountFailed = 1
		result.Items = append(result.Items, itemResult)
		return nil, result, errors.New("data import decision was rejected")
	}

	uniform := s.request.UniformSettings

	var account *service.Account
	var warnings []string
	var err error
	switch decision.Action {
	case dataImportActionUpdate:
		if decision.AccountID == nil {
			err = errors.New("data import update target is unavailable")
			break
		}
		existing, getErr := s.handler.adminService.GetAccount(ctx, *decision.AccountID)
		if getErr != nil || existing == nil {
			err = errors.New("data import update target changed")
			break
		}
		account, warnings, err = s.handler.updateImportedDataAccount(
			ctx, decision.Account, *existing, uniform, DataImportItemOverrides{}, s.taxonomy,
		)
		if err == nil {
			result.AccountUpdated = 1
		}
	case dataImportActionCreate:
		if matches := s.identityIndex.Find(dataAccountIdentityKeys(
			decision.Account.Platform, decision.Account.Credentials, decision.Account.Extra,
		)); len(matches) != 0 {
			err = errors.New("data import create identity changed")
			break
		}
		account, warnings, err = s.handler.createImportedDataAccount(
			ctx,
			decision.Account,
			uniform,
			DataImportItemOverrides{},
			s.proxyKeyToID,
			s.groups,
			s.taxonomy,
			s.skipDefaultGroupBind,
		)
		if err == nil {
			result.AccountCreated = 1
		}
	default:
		err = errors.New("data import action is invalid")
	}
	if err != nil || account == nil {
		if err == nil {
			err = errors.New("data import did not return an account")
		}
		itemResult.Code = dataImportCodeExecutionFailed
		itemResult.Message = err.Error()
		itemResult.Error = itemResult.Message
		result.AccountFailed = 1
		result.Items = append(result.Items, itemResult)
		return nil, result, err
	}

	accountID := account.ID
	itemResult.AccountID = &accountID
	itemResult.Warnings = append([]string(nil), warnings...)
	result.AccountIDs = append(result.AccountIDs, accountID)
	result.Items = append(result.Items, itemResult)
	return account, result, nil
}

func (s *dataImportJobState) recordCommittedAccount(account *service.Account) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	s.identityIndex.Add(*account)
}
