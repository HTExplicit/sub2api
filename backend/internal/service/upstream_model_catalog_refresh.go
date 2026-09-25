package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	upstreamModelCatalogRefreshInterval    = 15 * time.Minute
	upstreamModelCatalogRefreshInitialWait = 2 * time.Minute
	upstreamModelCatalogRefreshMaxAge      = 24 * time.Hour
	upstreamModelCatalogRefreshBatch       = 10
	upstreamModelCatalogRefreshSpacing     = 3 * time.Second
	upstreamModelCatalogRefreshBackoffBase = 30 * time.Minute
	upstreamModelCatalogSyncTimeout        = 90 * time.Second
	upstreamCapacityObservationMaxAge      = 24 * time.Hour
)

// upstreamModelCatalogSyncsInFlight holds the account IDs whose catalog sync is
// running in this process, so the periodic refresh and the create/credential
// trigger never sync one account twice at the same time.
var upstreamModelCatalogSyncsInFlight sync.Map

// UpstreamModelCatalogAutoSyncEligible reports whether an account's model list
// and the capacities it declares are refreshed automatically: active API-key
// accounts. OAuth accounts are never polled; their observations arrive with the
// Codex manifests that already pass through the gateway.
func UpstreamModelCatalogAutoSyncEligible(account *Account) bool {
	return account != nil && account.ID > 0 && account.Status == StatusActive && account.Type == AccountTypeAPIKey
}

// SyncUpstreamModelCatalogInBackground refreshes an API-key account right after
// it was created or its credentials changed, without delaying the admin
// response. The account is re-read so the sync uses the stored credentials.
func (s *AccountTestService) SyncUpstreamModelCatalogInBackground(accountID int64) {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("upstream_model_catalog_sync_panic", "account_id", accountID, "recover", recovered)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), upstreamModelCatalogSyncTimeout)
		defer cancel()
		account, err := s.accountRepo.GetByID(ctx, accountID)
		if err != nil || !UpstreamModelCatalogAutoSyncEligible(account) {
			return
		}
		_ = s.syncUpstreamModelCatalogOnce(ctx, account)
	}()
}

func (s *AccountTestService) syncUpstreamModelCatalogOnce(ctx context.Context, account *Account) error {
	if _, busy := upstreamModelCatalogSyncsInFlight.LoadOrStore(account.ID, struct{}{}); busy {
		return nil
	}
	defer upstreamModelCatalogSyncsInFlight.Delete(account.ID)
	if _, err := s.SyncUpstreamModelCatalog(ctx, account); err != nil {
		slog.Info("upstream_model_catalog_sync_failed", "account_id", account.ID, "platform", account.Platform, "error", err)
		return err
	}
	return nil
}

// upstreamModelCatalogSyncedAt reads when the account's catalog was last synced,
// including syncs that found nothing to keep.
func upstreamModelCatalogSyncedAt(account *Account) (time.Time, bool) {
	if account == nil || account.Extra == nil {
		return time.Time{}, false
	}
	body, err := json.Marshal(account.Extra[UpstreamModelMetadataExtraKey])
	if err != nil {
		return time.Time{}, false
	}
	var stored struct {
		SyncedAt string `json:"synced_at"`
	}
	if json.Unmarshal(body, &stored) != nil {
		return time.Time{}, false
	}
	syncedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(stored.SyncedAt))
	return syncedAt, err == nil
}

// UpstreamModelCatalogRefreshService re-syncs every eligible account whose
// catalog is older than a day, a few accounts per tick so upstream calls are
// staggered, and backs off accounts whose sync keeps failing.
type UpstreamModelCatalogRefreshService struct {
	accounts AccountRepository
	syncer   *AccountTestService
	now      func() time.Time

	mu      sync.Mutex
	backoff map[int64]upstreamModelCatalogBackoff

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type upstreamModelCatalogBackoff struct {
	failures int
	until    time.Time
}

func NewUpstreamModelCatalogRefreshService(accounts AccountRepository, syncer *AccountTestService) *UpstreamModelCatalogRefreshService {
	return &UpstreamModelCatalogRefreshService{
		accounts: accounts, syncer: syncer, now: time.Now,
		backoff: make(map[int64]upstreamModelCatalogBackoff), stopCh: make(chan struct{}),
	}
}

func (s *UpstreamModelCatalogRefreshService) Start() {
	if s == nil || s.accounts == nil || s.syncer == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		wait := upstreamModelCatalogRefreshInitialWait
		for {
			select {
			case <-s.stopCh:
				return
			case <-time.After(wait):
			}
			s.runOnce()
			wait = upstreamModelCatalogRefreshInterval
		}
	}()
}

func (s *UpstreamModelCatalogRefreshService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *UpstreamModelCatalogRefreshService) runOnce() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	accounts, err := s.accounts.ListActive(ctx)
	if err != nil {
		slog.Warn("upstream_model_catalog_refresh_list_failed", "error", err)
		return
	}
	for i, account := range s.dueAccounts(accounts) {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(upstreamModelCatalogRefreshSpacing):
			}
		}
		syncCtx, syncCancel := context.WithTimeout(ctx, upstreamModelCatalogSyncTimeout)
		err := s.syncer.syncUpstreamModelCatalogOnce(syncCtx, account)
		syncCancel()
		if ctx.Err() != nil {
			return
		}
		s.recordResult(account.ID, err)
	}
}

// dueAccounts returns the eligible accounts never synced or synced more than a
// day ago that are not backing off, oldest first, at most one batch.
func (s *UpstreamModelCatalogRefreshService) dueAccounts(accounts []Account) []*Account {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	type dueAccount struct {
		account  *Account
		syncedAt time.Time
	}
	due := make([]dueAccount, 0)
	for i := range accounts {
		account := &accounts[i]
		if !UpstreamModelCatalogAutoSyncEligible(account) || now.Before(s.backoff[account.ID].until) {
			continue
		}
		syncedAt, synced := upstreamModelCatalogSyncedAt(account)
		if synced && now.Sub(syncedAt) < upstreamModelCatalogRefreshMaxAge {
			continue
		}
		due = append(due, dueAccount{account: account, syncedAt: syncedAt})
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].syncedAt.Before(due[j].syncedAt) })
	result := make([]*Account, 0, min(len(due), upstreamModelCatalogRefreshBatch))
	for _, item := range due {
		if len(result) == upstreamModelCatalogRefreshBatch {
			break
		}
		result = append(result, item.account)
	}
	return result
}

func (s *UpstreamModelCatalogRefreshService) recordResult(accountID int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.backoff, accountID)
		return
	}
	state := s.backoff[accountID]
	state.failures++
	delay := upstreamModelCatalogRefreshBackoffBase << min(state.failures-1, 6)
	state.until = s.now().Add(min(delay, upstreamModelCatalogRefreshMaxAge))
	s.backoff[accountID] = state
}

// upstreamCapacityObservationMarks remembers the observation this process last
// wrote per account, so repeated manifest refreshes do not rewrite an unchanged
// snapshot before the scheduler snapshot has caught up with the write.
var upstreamCapacityObservationMarks sync.Map

type upstreamCapacityObservationMark struct {
	digest string
	at     time.Time
}

// recordUpstreamModelCapacityObservations stores the capacities an upstream
// response declared for the account. OAuth accounts are updated this way when
// their Codex manifest passes through the gateway. Only capacity fields change,
// and only when a value changed or the stored observation is a day old.
func recordUpstreamModelCapacityObservations(ctx context.Context, repo AccountRepository, account *Account, body []byte) {
	if repo == nil || account == nil || account.ID <= 0 || len(body) == 0 {
		return
	}
	observed, err := ParseUpstreamModelContextCapacities(body, account.Platform)
	if err != nil || len(observed) == 0 {
		return
	}
	now := time.Now().UTC()
	digestBody, _ := json.Marshal(observed)
	digest := string(digestBody)
	if value, ok := upstreamCapacityObservationMarks.Load(account.ID); ok {
		if mark, ok := value.(upstreamCapacityObservationMark); ok && mark.digest == digest && now.Sub(mark.at) < upstreamCapacityObservationMaxAge {
			return
		}
	}
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		snapshot = &UpstreamModelMetadataSnapshot{Source: "upstream", Models: make(map[string]UpstreamModelMetadata)}
	}
	stamp := now.Format(time.RFC3339)
	changed := false
	for modelID, capacity := range observed {
		declared := upstreamCapacityDeclaration(capacity, stamp)
		entry, exists := snapshot.Models[modelID]
		if exists && entry.CapacitySource == ModelContextSourceUpstream && entry.ContextWindow == declared.ContextWindow &&
			entry.MaxContextWindow == declared.MaxContextWindow && entry.MaxInputTokens == declared.MaxInputTokens &&
			entry.MaxOutputTokens == declared.MaxOutputTokens {
			if observedAt, parseErr := time.Parse(time.RFC3339, entry.ObservedAt); parseErr == nil && now.Sub(observedAt) < upstreamCapacityObservationMaxAge {
				continue
			}
		}
		if !exists {
			entry = UpstreamModelMetadata{ID: modelID}
		}
		snapshot.Models[modelID] = withUpstreamModelCapacity(entry, declared)
		changed = true
	}
	if changed {
		if snapshot.SyncedAt == "" {
			snapshot.SyncedAt = stamp
		}
		if err := repo.UpdateExtra(ctx, account.ID, map[string]any{UpstreamModelMetadataExtraKey: *snapshot}); err != nil {
			slog.Warn("upstream_capacity_observation_save_failed", "account_id", account.ID, "error", err)
			return
		}
	}
	upstreamCapacityObservationMarks.Store(account.ID, upstreamCapacityObservationMark{digest: digest, at: now})
}
