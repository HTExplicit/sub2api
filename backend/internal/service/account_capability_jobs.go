package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

type AccountCapabilityService struct {
	repo         AccountCapabilityRepository
	accounts     AccountRepository
	executor     AccountCapabilityExecutor
	mu           sync.Mutex
	cancel       context.CancelFunc
	releaseLease func()
	wg           sync.WaitGroup
}

func NewAccountCapabilityService(repo AccountCapabilityRepository, accounts AccountRepository, executor AccountCapabilityExecutor) *AccountCapabilityService {
	return &AccountCapabilityService{repo: repo, accounts: accounts, executor: executor}
}

func (s *AccountCapabilityService) Create(ctx context.Context, actorID int64, key string, request AccountCapabilityCreateRequest) (*AccountCapabilityRun, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 255 {
		return nil, false, ErrAccountCapabilityIdempotencyRequired
	}
	if actorID <= 0 {
		return nil, false, ErrAccountCapabilityInvalid
	}
	request, err := normalizeCapabilityRequest(request)
	if err != nil {
		return nil, false, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, false, ErrAccountCapabilityInvalid
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	if existing, findErr := s.repo.FindIdempotent(ctx, actorID, key); findErr == nil {
		if existing.RequestHash != hash {
			return nil, false, ErrAccountCapabilityIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(findErr, ErrAccountCapabilityNotFound) {
		return nil, false, findErr
	}
	accounts, err := s.accounts.GetByIDs(ctx, request.AccountIDs)
	if err != nil {
		return nil, false, err
	}
	byID := make(map[int64]*Account, len(accounts))
	folders := make(map[int64]bool, len(request.FolderIDs))
	for _, id := range request.FolderIDs {
		folders[id] = true
	}
	for _, account := range accounts {
		if account == nil || account.ManagementFolderID == nil || !folders[*account.ManagementFolderID] {
			return nil, false, ErrAccountCapabilityScope
		}
		byID[account.ID] = account
	}
	if len(byID) != len(request.AccountIDs) {
		return nil, false, ErrAccountCapabilityScope
	}
	items := make([]AccountCapabilityItem, 0, len(request.Items))
	appendItem := func(id int64, target AccountCapabilityProbeTarget) error {
		account := byID[id]
		if account == nil {
			return ErrAccountCapabilityScope
		}
		fingerprint, fingerprintErr := AccountCapabilityFingerprint(account)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		if expected := request.ExpectedConfigFingerprints[id]; expected != "" && expected != fingerprint {
			return ErrAccountCapabilityScope
		}
		if request.OnlyUntested && (!ManagedModelBranchProtocolSupported(account.Platform, target.Protocol) || len(CapabilityIngressEndpoints(account, target.Protocol)) == 0) {
			// Organizer jobs may only spend a basic request on a wire that the
			// managed router can use. Manual diagnostic jobs remain separate.
			return ErrAccountCapabilityInvalid
		}
		items = append(items, AccountCapabilityItem{Ordinal: len(items) + 1, Kind: request.Kind, AccountID: id,
			AccountName: account.Name, FolderID: *account.ManagementFolderID, ConfigFingerprint: fingerprint,
			UpstreamModel: target.UpstreamModel, Protocol: target.Protocol, Profile: target.Profile,
			Aliases: target.Aliases, Status: "pending", Result: json.RawMessage(`{}`)})
		return nil
	}
	if request.Kind == AccountCapabilityKindDiscover {
		for _, id := range request.AccountIDs {
			if err := appendItem(id, AccountCapabilityProbeTarget{Profile: "text", Aliases: []string{}}); err != nil {
				return nil, false, err
			}
		}
	} else {
		for _, target := range request.Items {
			if err := appendItem(target.AccountID, target); err != nil {
				return nil, false, err
			}
		}
	}
	return s.repo.Create(ctx, &AccountCapabilityRun{CreatedBy: actorID, Kind: request.Kind, IdempotencyKey: key,
		RequestHash: hash, OnlyUntested: request.OnlyUntested, FolderIDs: request.FolderIDs, AccountIDs: request.AccountIDs, Status: "pending", TargetCount: len(items)}, items)
}

func normalizeCapabilityRequest(request AccountCapabilityCreateRequest) (AccountCapabilityCreateRequest, error) {
	if request.Kind != AccountCapabilityKindDiscover && request.Kind != AccountCapabilityKindProbe {
		return request, ErrAccountCapabilityInvalid
	}
	var err error
	request.FolderIDs, err = capabilityIDs(request.FolderIDs)
	if err != nil || len(request.FolderIDs) == 0 {
		return request, ErrAccountCapabilityInvalid
	}
	request.AccountIDs, err = capabilityIDs(request.AccountIDs)
	if err != nil || len(request.AccountIDs) == 0 || len(request.AccountIDs) > 1000 {
		return request, ErrAccountCapabilityInvalid
	}
	if request.Kind == AccountCapabilityKindDiscover {
		if len(request.Items) != 0 || request.OnlyUntested || len(request.ExpectedConfigFingerprints) != 0 {
			return request, ErrAccountCapabilityInvalid
		}
		return request, nil
	}
	if len(request.Items) == 0 {
		return request, ErrAccountCapabilityInvalid
	}
	accountSet := map[int64]bool{}
	for _, id := range request.AccountIDs {
		accountSet[id] = true
	}
	for id, fingerprint := range request.ExpectedConfigFingerprints {
		if !accountSet[id] || len(fingerprint) != 64 {
			return request, ErrAccountCapabilityInvalid
		}
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return request, ErrAccountCapabilityInvalid
		}
	}
	targets := make(map[string]AccountCapabilityProbeTarget)
	untestedProtocols := make(map[string]string)
	for _, target := range request.Items {
		if !accountSet[target.AccountID] || !validCapabilityModelID(target.UpstreamModel) {
			return request, ErrAccountCapabilityInvalid
		}
		switch target.Protocol {
		case "responses", "responses_websocket", "chat_completions", "messages", "responses_input_tokens", "messages_count_tokens":
		default:
			return request, ErrAccountCapabilityInvalid
		}
		if target.Profile == "" {
			target.Profile = "text"
		}
		if target.Profile != "text" && target.Profile != "tool_roundtrip" {
			return request, ErrAccountCapabilityInvalid
		}
		if request.OnlyUntested {
			if target.Profile != "text" || request.ExpectedConfigFingerprints[target.AccountID] == "" ||
				(target.Protocol != "responses" && target.Protocol != "chat_completions" && target.Protocol != "messages") {
				return request, ErrAccountCapabilityInvalid
			}
			modelIdentity, _ := json.Marshal([]any{target.AccountID, target.UpstreamModel})
			if previous, found := untestedProtocols[string(modelIdentity)]; found && previous != target.Protocol {
				return request, ErrAccountCapabilityInvalid
			}
			untestedProtocols[string(modelIdentity)] = target.Protocol
		}
		if (target.Protocol == "responses_input_tokens" || target.Protocol == "messages_count_tokens") && target.Profile != "text" {
			return request, ErrAccountCapabilityInvalid
		}
		for _, alias := range target.Aliases {
			if !validCapabilityModelID(alias) {
				return request, ErrAccountCapabilityInvalid
			}
		}
		keyBytes, _ := json.Marshal([]any{target.AccountID, target.UpstreamModel, target.Protocol, target.Profile})
		key := string(keyBytes)
		if previous, ok := targets[key]; ok {
			target.Aliases = append(target.Aliases, previous.Aliases...)
		}
		target.Aliases = capabilityStrings(target.Aliases)
		targets[key] = target
	}
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	request.Items = make([]AccountCapabilityProbeTarget, 0, len(keys))
	for _, key := range keys {
		request.Items = append(request.Items, targets[key])
	}
	return request, nil
}

func validCapabilityModelID(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\t\x00")
}

func capabilityIDs(input []int64) ([]int64, error) {
	set := make(map[int64]bool, len(input))
	for _, id := range input {
		if id <= 0 {
			return nil, ErrAccountCapabilityInvalid
		}
		set[id] = true
	}
	result := make([]int64, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func capabilityStrings(input []string) []string {
	set := map[string]bool{}
	for _, value := range input {
		set[value] = true
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s *AccountCapabilityService) GetRun(ctx context.Context, id int64) (*AccountCapabilityRun, error) {
	return s.repo.GetRun(ctx, id)
}

// FindCreationReceipt resolves an uncertain submission without replaying its
// request. A missing receipt does not prove an in-flight create cannot commit.
func (s *AccountCapabilityService) FindCreationReceipt(ctx context.Context, actorID int64, key string) (*AccountCapabilityRun, error) {
	key = strings.TrimSpace(key)
	if actorID <= 0 {
		return nil, ErrAccountCapabilityInvalid
	}
	if key == "" || len(key) > 255 {
		return nil, ErrAccountCapabilityIdempotencyRequired
	}
	return s.repo.FindIdempotent(ctx, actorID, key)
}
func (s *AccountCapabilityService) ListRuns(ctx context.Context, filter AccountCapabilityFilter) (*AccountCapabilityRunPage, error) {
	return s.repo.ListRuns(ctx, filter)
}
func (s *AccountCapabilityService) GetItemsByIDs(ctx context.Context, ids []int64) ([]AccountCapabilityItem, error) {
	return s.repo.GetItemsByIDs(ctx, ids)
}

func (s *AccountCapabilityService) ListItems(ctx context.Context, id int64, filter AccountCapabilityFilter) (*AccountCapabilityItemPage, error) {
	if _, err := s.repo.GetRun(ctx, id); err != nil {
		return nil, err
	}
	page, err := s.repo.ListItems(ctx, id, filter)
	if err != nil {
		return nil, err
	}
	if err := s.decorateItems(ctx, page.Items); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *AccountCapabilityService) Inventory(ctx context.Context, filter AccountCapabilityFilter) (*AccountCapabilityItemPage, error) {
	page, err := s.repo.LatestItems(ctx, filter)
	if err != nil {
		return nil, err
	}
	if err := s.decorateItems(ctx, page.Items); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *AccountCapabilityService) decorateItems(ctx context.Context, items []AccountCapabilityItem) error {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.AccountID)
	}
	ids, _ = capabilityIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	accounts, err := s.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return err
	}
	byID := map[int64]*Account{}
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	for i := range items {
		account := byID[items[i].AccountID]
		fingerprint, fingerprintErr := AccountCapabilityFingerprint(account)
		items[i].StaleConfig = fingerprintErr != nil || fingerprint != items[i].ConfigFingerprint
		items[i].IsCurrentScope = AccountCapabilityAccountInScope(account, &items[i])
	}
	return nil
}

func (s *AccountCapabilityService) Control(ctx context.Context, id int64, action string) (*AccountCapabilityRun, error) {
	if action != "pause" && action != "resume" && action != "cancel" {
		return nil, ErrAccountCapabilityInvalid
	}
	if action == "resume" {
		items, err := s.repo.ScopeItems(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := s.decorateItems(ctx, items); err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.StaleConfig || !item.IsCurrentScope {
				return nil, ErrAccountCapabilityScope
			}
		}
	}
	return s.repo.Control(ctx, id, action)
}

func (s *AccountCapabilityService) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil
	}
	if s.repo == nil || s.accounts == nil || s.executor == nil {
		return ErrAccountCapabilityInvalid
	}
	if leaseRepo, ok := s.repo.(AccountCapabilityRuntimeLeaseRepository); ok {
		release, acquired, err := leaseRepo.AcquireRuntimeLease(parent)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		s.releaseLease = release
	}
	if err := s.repo.RecoverInterrupted(parent); err != nil {
		if s.releaseLease != nil {
			s.releaseLease()
			s.releaseLease = nil
		}
		return err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s.cancel = cancel
	for range AccountCapabilityWorkers {
		s.wg.Add(1)
		go s.worker(ctx)
	}
	return nil
}

func (s *AccountCapabilityService) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releaseLease != nil {
		s.releaseLease()
		s.releaseLease = nil
	}
	s.cancel = nil
}

func (s *AccountCapabilityService) worker(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		item, err := s.repo.Claim(ctx)
		if err == nil && item != nil {
			s.executeItem(ctx, item)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *AccountCapabilityService) executeItem(ctx context.Context, item *AccountCapabilityItem) {
	complete := func(status string, result json.RawMessage, count int) {
		// Persist the observation even if the client disconnected or shutdown canceled the request.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		for attempt := 0; attempt < 3; attempt++ {
			if err := s.repo.Complete(finishCtx, item.ID, status, result, count); err == nil {
				return
			}
			if attempt < 2 {
				timer := time.NewTimer(200 * time.Millisecond)
				select {
				case <-finishCtx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
		}
		// Retrying persistence never resends the upstream request. Stop this run
		// from spending more while an observation could not be committed.
		_, _ = s.repo.Control(finishCtx, item.RunID, "pause")
		slog.Error("account_capability_result_persistence_failed", "run_id", item.RunID, "item_id", item.ID)
	}
	account, err := s.accounts.GetByID(ctx, item.AccountID)
	if err != nil || !AccountCapabilityAccountInScope(account, item) {
		complete("stale", capabilityLocalResult("stale_config", "scope_changed"), 0)
		return
	}
	fingerprint, err := AccountCapabilityFingerprint(account)
	if err != nil || fingerprint != item.ConfigFingerprint {
		complete("stale", capabilityLocalResult("stale_config", "configuration_changed"), 0)
		return
	}
	dispatched, err := s.repo.MarkDispatched(ctx, item.ID)
	if err != nil {
		complete("failed", capabilityLocalResult("failed", "dispatch_state_unavailable"), 0)
		return
	} // A restart safely distinguishes reserved from possibly sent.
	if !dispatched {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.repo.Release(releaseCtx, item.ID)
		return
	}
	itemTimeout := AccountCapabilityRequestTimeout
	if item.Kind == AccountCapabilityKindProbe && item.Profile == "tool_roundtrip" {
		itemTimeout *= 2
	}
	probeCtx, cancel := context.WithTimeout(ctx, itemTimeout)
	defer cancel()
	var raw json.RawMessage
	var resultStatus string
	var count int
	if item.Kind == AccountCapabilityKindDiscover {
		result := s.executor.Discover(probeCtx, account)
		raw, err = json.Marshal(result)
		resultStatus, count = result.Status, result.RequestCount
	} else {
		result := s.executor.Probe(probeCtx, account, item.UpstreamModel, item.Protocol, item.Profile)
		raw, err = json.Marshal(result)
		resultStatus, count = result.Status, result.RequestCount
	}
	if err != nil || ValidateAccountJobMetadata(raw) != nil {
		complete("indeterminate", capabilityLocalResult("uncertain", "result_unavailable"), count)
		return
	}
	status := "failed"
	switch resultStatus {
	case "alive", "discovered", "empty", "partial", "available":
		status = "succeeded"
	case "uncertain":
		status = "indeterminate"
	case "canceled":
		status = "indeterminate"
	}
	complete(status, raw, count)
}

func capabilityLocalResult(status, classification string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"status": status, "classification": classification, "reason": "Capability observation was not completed; no automatic retry is allowed."})
	return raw
}
