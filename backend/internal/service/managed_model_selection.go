package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"
)

type ManagedModelCandidate struct {
	Branch  ManagedModelRouteBranch
	Account *Account
}

func (candidate ManagedModelCandidate) Key() string {
	if candidate.Account == nil {
		return candidate.Branch.Selector + "/0"
	}
	return candidate.Branch.Selector + "/" + strconv.FormatInt(candidate.Account.ID, 10)
}

// ListManagedModelCandidates hydrates every legitimate path. A map keyed only
// by account ID would silently erase same-account alternate real targets.
func (s *GatewayService) ListManagedModelCandidates(ctx context.Context, request *ManagedModelRequest) ([]ManagedModelCandidate, error) {
	if s == nil || s.accountRepo == nil || request == nil || request.GroupID <= 0 {
		return nil, ErrManagedModelRouteUnavailable
	}
	ctx = WithManagedModelRequest(ctx, request)
	ids := make([]int64, 0)
	seenIDs := make(map[int64]bool)
	for _, branch := range ManagedModelRouteBranches(request.Route) {
		if !managedModelHasEndpoint(branch.Endpoints, request.Endpoint) { continue }
		for _, member := range branch.Accounts {
			if member.AccountID > 0 && !seenIDs[member.AccountID] {
				ids = append(ids, member.AccountID)
				seenIDs[member.AccountID] = true
			}
		}
	}
	if len(ids) == 0 { return nil, nil }
	loaded, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil { return nil, ErrManagedModelRouteUnavailable }
	accounts := make(map[int64]*Account, len(loaded))
	for _, account := range loaded {
		if account != nil && seenIDs[account.ID] { accounts[account.ID] = account }
	}
	seen := make(map[string]bool)
	candidates := make([]ManagedModelCandidate, 0)
	for _, branch := range ManagedModelRouteBranches(request.Route) {
		if !managedModelHasEndpoint(branch.Endpoints, request.Endpoint) {
			continue
		}
		branchCtx := WithManagedModelBranch(ctx, branch)
		for _, member := range branch.Accounts {
			account := accounts[member.AccountID]
			if account == nil || account.SchedulerMetadata != nil || (branch.UpstreamProtocol != "" && account.Type != AccountTypeAPIKey) || !account.IsSchedulableForModelWithContext(branchCtx, branch.Selector) || !managedModelAccountInGroup(account, request.GroupID) || !ManagedModelAccountAllowed(branchCtx, account, branch.Selector) {
				continue
			}
			if account.ProxyID != nil && (account.Proxy == nil || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now())) {
				continue
			}
			candidate := ManagedModelCandidate{Branch: branch, Account: account}
			if !seen[candidate.Key()] {
				candidates = append(candidates, candidate)
				seen[candidate.Key()] = true
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Account, candidates[j].Account
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.LastUsedAt == nil && b.LastUsedAt != nil {
			return true
		}
		if a.LastUsedAt != nil && b.LastUsedAt == nil {
			return false
		}
		if a.LastUsedAt != nil && b.LastUsedAt != nil && !a.LastUsedAt.Equal(*b.LastUsedAt) {
			return a.LastUsedAt.Before(*b.LastUsedAt)
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return candidates[i].Branch.Selector < candidates[j].Branch.Selector
	})
	return candidates, nil
}

type ManagedModelSelectionOptions struct {
	Excluded           map[string]struct{}
	PreferredBranch    string
	PreferredAccountID int64
	SessionHash        string
	MetadataUserID     string
	UserID             int64
	RequireCompact     bool
}

type ManagedModelSelection struct {
	Candidate ManagedModelCandidate
	Selection *AccountSelectionResult
	Context   context.Context
}

// SelectManagedModelCandidate orders the cross-platform graph, then delegates
// each candidate's admission to the normal scheduler. Account quota, profit,
// privacy, load, concurrency, session limits and breaker gates are not replaced
// with a publication-only boolean. Exclusions remain path-local, so a failure
// of one real target does not erase another verified target on the same account.
func (s *GatewayService) SelectManagedModelCandidate(ctx context.Context, openAI *OpenAIGatewayService, request *ManagedModelRequest, options ManagedModelSelectionOptions) (*ManagedModelSelection, error) {
	candidates, err := s.ListManagedModelCandidates(ctx, request)
	if err != nil {
		return nil, err
	}
	var waiting *ManagedModelSelection
	release := func(selection *ManagedModelSelection, keepSessionAccountID int64) {
		if selection == nil || selection.Selection == nil {
			return
		}
		if selection.Selection.ReleaseFunc != nil {
			selection.Selection.ReleaseFunc()
		}
		if selection.Candidate.Branch.TargetPlatform == PlatformOpenAI {
			if openAI != nil {
				openAI.ReleaseOpenAIRuntimeBreakerProbeForSelection(selection.Selection)
			}
		} else if selection.Selection.Account != nil && selection.Selection.Account.ID != keepSessionAccountID {
			s.ReleaseAccountSession(selection.Context, selection.Selection.Account, options.SessionHash)
		}
	}
	defer func() { release(waiting, 0) }()
	for _, candidate := range candidates {
		if _, excluded := options.Excluded[candidate.Key()]; excluded {
			continue
		}
		if options.PreferredAccountID > 0 && candidate.Account.ID != options.PreferredAccountID {
			continue
		}
		if options.PreferredBranch != "" && candidate.Branch.Selector != options.PreferredBranch {
			continue
		}
		if options.RequireCompact && (candidate.Branch.TargetPlatform != PlatformOpenAI || candidate.Branch.UpstreamProtocol == CompositeRouteEndpointChatCompletions || (candidate.Branch.UpstreamProtocol == "" && shouldForwardOpenAIResponsesViaRawChatCompletions(candidate.Account))) {
			continue
		}
		branchCtx := WithManagedModelBranch(WithManagedModelRequest(ctx, request), candidate.Branch)
		// The public group, not the adapter's platform, owns the profit gate.
		// OpenAI's legacy gate alone intentionally covers only OpenAI/Grok groups.
		branchCtx = s.withGatewayProfitControlGate(branchCtx, &request.GroupID)
		excluded := make(map[int64]struct{}, len(candidate.Branch.Accounts))
		for _, member := range candidate.Branch.Accounts {
			if member.AccountID != candidate.Account.ID {
				excluded[member.AccountID] = struct{}{}
			}
		}
		var selection *AccountSelectionResult
		if candidate.Branch.TargetPlatform == PlatformOpenAI {
			if openAI == nil {
				return nil, ErrManagedModelRouteUnavailable
			}
			selection, _, err = openAI.SelectAccountWithSchedulerForCapability(branchCtx, &request.GroupID, "", options.SessionHash, candidate.Branch.Selector, excluded, OpenAIUpstreamTransportHTTPSSE, "", options.RequireCompact, false, true, PlatformOpenAI)
		} else {
			selection, err = s.SelectAccountWithLoadAwareness(branchCtx, &request.GroupID, options.SessionHash, candidate.Branch.Selector, excluded, options.MetadataUserID, options.UserID)
		}
		if err != nil {
			if errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts) {
				continue
			}
			return nil, err
		}
		if selection == nil || selection.Account == nil {
			continue
		}
		if selection.Account.ID != candidate.Account.ID || ValidateManagedModelAccount(branchCtx, selection.Account, candidate.Branch.Selector) != nil {
			release(&ManagedModelSelection{Candidate: candidate, Selection: selection, Context: branchCtx}, 0)
			continue
		}
		candidate.Account = selection.Account
		result := &ManagedModelSelection{Candidate: candidate, Selection: selection, Context: ContextWithSelectionProfitGate(branchCtx, selection)}
		if !selection.Acquired && selection.WaitPlan != nil {
			if waiting == nil {
				waiting = result
			} else {
				release(result, waiting.Selection.Account.ID)
			}
			continue
		}
		if !selection.Acquired {
			release(result, 0)
			continue
		}
		release(waiting, selection.Account.ID)
		waiting = nil
		return result, nil
	}
	if waiting != nil {
		result := waiting
		waiting = nil
		return result, nil
	}
	return nil, ErrNoAvailableAccounts
}

// HydrateManagedModelAccount is the post-slot boundary. A profit refresh may
// have returned lightweight scheduler metadata; actual forwarding always needs
// a current full account and exact published mapping, not the cached digest.
func (s *GatewayService) HydrateManagedModelAccount(ctx context.Context, accountID int64, selector string) (*Account, error) {
	request, ok := ManagedModelRequestFromContext(ctx)
	if s == nil || s.accountRepo == nil || !ok || request == nil {
		return nil, ErrManagedModelRouteUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil || account.SchedulerMetadata != nil || !account.IsSchedulableForModelWithContext(ctx, selector) || !managedModelAccountInGroup(account, request.GroupID) || !ManagedModelAccountAllowed(ctx, account, selector) {
		return nil, ErrManagedModelRouteUnavailable
	}
	if account.ProxyID != nil && (account.Proxy == nil || !account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now())) {
		return nil, ErrManagedModelRouteUnavailable
	}
	return account, nil
}
