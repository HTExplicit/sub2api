package service

import (
	"context"
	"errors"
	"reflect"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var errCindyGroupIdentityUnavailable = errors.New("cindy group identity is unavailable")

// cindyGroupIdentityClassifier is implemented by the production repository
// with one aggregate query. Authentication cache misses use it to materialize
// an exact identity marker in the versioned API-key snapshot.
type cindyGroupIdentityClassifier interface {
	ClassifyStrictCindyGroup(ctx context.Context, groupID int64) (bool, error)
}

// cindyGroupAccountReader is the explicit, test-friendly fallback. The marker
// prevents promoted calls through legacy test doubles that anonymously embed a
// nil AccountRepository.
type cindyGroupAccountReader interface {
	ListCindyGroupIdentityMembers(ctx context.Context, groupID int64) ([]Account, error)
	CindyGroupIdentityReaderMarker()
}

// cindySchedulableGroupAccountReader is deliberately narrower than
// AccountRepository. Requiring the explicit marker prevents legacy test
// doubles that anonymously embed a nil AccountRepository from satisfying this
// boundary through a promoted ListSchedulableByGroupID method and panicking.
type cindySchedulableGroupAccountReader interface {
	ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error)
	CindyGroupIdentityReaderMarker()
}

// cindyCodexModelsAccountReader is a narrow, explicitly attested dependency
// for deterministic mixed-group model discovery. The marker prevents a test
// double with an anonymously embedded nil AccountRepository from satisfying
// the interface through promoted methods and panicking at runtime.
type cindyCodexModelsAccountReader interface {
	ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error)
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
	CindyCodexModelsAccountReaderMarker()
}

// CindyCodexModelsScope describes how /v1/models?client_version should expose
// the verified Cindy Codex surface without making its result depend on a
// randomly selected Cindy account.
type CindyCodexModelsScope struct {
	CatalogOnly        bool
	MergeCatalog       bool
	OrdinaryAccountIDs []int64
	ExcludedAccountIDs []int64
}

// classifyStrictCindyGroup is the explicit legacy/non-auth fallback. Normal
// gateway requests consume the marker from the API-key auth snapshot instead;
// account identity and membership transactions enqueue durable cross-instance
// snapshot invalidations rather than relying on a stale process-local TTL.
func classifyStrictCindyGroup(ctx context.Context, repo any, groupID *int64) (bool, error) {
	if groupID == nil || *groupID <= 0 {
		return false, nil
	}
	if classifier, ok := repo.(cindyGroupIdentityClassifier); ok && !isNilCindyGroupDependency(classifier) {
		return classifier.ClassifyStrictCindyGroup(ctx, *groupID)
	}
	reader, ok := asCindyGroupAccountReader(repo)
	if !ok {
		// Partial unit-test wiring historically omits a complete repository. In
		// production the concrete account repository always implements the
		// aggregate classifier above.
		return false, nil
	}
	return loadStrictCindyGroup(ctx, reader, *groupID)
}

func isStrictCindyGroup(ctx context.Context, repo any, groupID *int64) bool {
	strict, err := classifyStrictCindyGroup(ctx, repo, groupID)
	return err == nil && strict
}

func loadStrictCindyGroup(ctx context.Context, repo cindyGroupAccountReader, groupID int64) (bool, error) {
	// Group identity depends on every non-deleted membership, regardless of
	// enabled or transient schedulability state. This matches admin audit/split.
	accounts, err := repo.ListCindyGroupIdentityMembers(ctx, groupID)
	if err != nil {
		return false, err
	}
	if len(accounts) == 0 {
		return false, nil
	}
	for i := range accounts {
		account := &accounts[i]
		if !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			return false, nil
		}
	}
	return true, nil
}

func asCindyGroupAccountReader(repo any) (cindyGroupAccountReader, bool) {
	reader, ok := repo.(cindyGroupAccountReader)
	if !ok || isNilCindyGroupDependency(reader) {
		return nil, false
	}
	return reader, true
}

func isNilCindyGroupDependency(repo any) bool {
	if repo == nil {
		return true
	}
	value := reflect.ValueOf(repo)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func classifyAuthenticatedCindyIdentityGroup(ctx context.Context, repo any, group *Group) (bool, error) {
	if group == nil || group.ID <= 0 {
		return false, nil
	}
	if group.Platform == PlatformCindy {
		return group.EffectiveWirePlatform() == WirePlatformOpenAI &&
			group.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1, nil
	}
	if group.Platform != PlatformOpenAI {
		return false, nil
	}
	if group.StrictCindyKnown {
		return group.StrictCindy, nil
	}
	groupID := group.ID
	return classifyStrictCindyGroup(ctx, repo, &groupID)
}

func classifyAuthenticatedStrictCindyGroup(ctx context.Context, repo any, group *Group) (bool, error) {
	// Disabling the capability catalogue is the routing rollback switch. Keep
	// the materialized identity in the auth snapshot, but bypass all strict
	// catalogue/protocol gates so the request follows the legacy generic path.
	if !CindyCapabilityCatalogFeatureEnabled() {
		return false, nil
	}
	return classifyAuthenticatedCindyIdentityGroup(ctx, repo, group)
}

func hasSchedulableCindyAccount(ctx context.Context, repo any, group *Group) (bool, error) {
	if !CindyCapabilityCatalogFeatureEnabled() {
		return false, nil
	}
	if group == nil || group.ID <= 0 || (group.Platform != PlatformOpenAI && group.Platform != PlatformCindy) {
		return false, nil
	}

	reader, ok := repo.(cindySchedulableGroupAccountReader)
	if !ok || isNilCindyGroupDependency(reader) {
		return false, errCindyGroupIdentityUnavailable
	}
	accounts, err := reader.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		return false, err
	}
	for i := range accounts {
		account := &accounts[i]
		if IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			return true, nil
		}
	}
	return false, nil
}

func (s *GatewayService) ClassifyStrictCindyGroup(ctx context.Context, group *Group) (bool, error) {
	if s == nil {
		return false, errCindyGroupIdentityUnavailable
	}
	return classifyAuthenticatedStrictCindyGroup(ctx, s.accountRepo, group)
}

// HasSchedulableCindyAccount reports whether a mixed group currently has an
// exact Cindy account eligible for scheduling. Repository uncertainty is an
// error so callers cannot accidentally publish Cindy capabilities fail-open.
func (s *GatewayService) HasSchedulableCindyAccount(ctx context.Context, group *Group) (bool, error) {
	if s == nil {
		return false, errCindyGroupIdentityUnavailable
	}
	return hasSchedulableCindyAccount(ctx, s.accountRepo, group)
}

func (s *OpenAIGatewayService) ClassifyStrictCindyGroup(ctx context.Context, group *Group) (bool, error) {
	if s == nil {
		return false, errCindyGroupIdentityUnavailable
	}
	return classifyAuthenticatedStrictCindyGroup(ctx, s.accountRepo, group)
}

// ClassifyCindyIdentityGroup returns the exact Cindy identity independently
// from capability-catalog rollout. Compatibility aliases use it, while
// catalog and protocol gates keep using ClassifyStrictCindyGroup.
func (s *OpenAIGatewayService) ClassifyCindyIdentityGroup(ctx context.Context, group *Group) (bool, error) {
	if s == nil {
		return false, errCindyGroupIdentityUnavailable
	}
	return classifyAuthenticatedCindyIdentityGroup(ctx, s.accountRepo, group)
}

// ResolveCindyCodexModelsScope determines the catalog policy before account
// selection. Strict groups use the local verified catalog. Mixed groups with a
// schedulable exact Cindy account fetch the legacy manifest from a non-Cindy
// account and merge the local catalog into it. Repository uncertainty fails
// closed so a random Cindy upstream manifest cannot leak live or unverified IDs.
func (s *OpenAIGatewayService) ResolveCindyCodexModelsScope(ctx context.Context, group *Group) (CindyCodexModelsScope, error) {
	var scope CindyCodexModelsScope
	if !CindyCapabilityCatalogFeatureEnabled() || group == nil || group.ID <= 0 {
		return scope, nil
	}
	if s == nil {
		return scope, errCindyGroupIdentityUnavailable
	}
	if group.Platform == PlatformCindy {
		cindyIdentity, err := classifyAuthenticatedCindyIdentityGroup(ctx, s.accountRepo, group)
		if err != nil {
			return scope, err
		}
		scope.CatalogOnly = cindyIdentity
		return scope, nil
	}
	if group.Platform != PlatformOpenAI {
		return scope, nil
	}

	strict, err := classifyAuthenticatedStrictCindyGroup(ctx, s.accountRepo, group)
	if err != nil {
		return scope, err
	}
	if strict {
		scope.CatalogOnly = true
		return scope, nil
	}

	reader, ok := s.accountRepo.(cindyCodexModelsAccountReader)
	if !ok || isNilCindyGroupDependency(reader) {
		return scope, errCindyGroupIdentityUnavailable
	}
	var accounts []Account
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		accounts, err = reader.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	} else {
		accounts, err = reader.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, PlatformOpenAI)
	}
	if err != nil {
		return scope, err
	}

	cindyAccountIDs := make([]int64, 0, len(accounts))
	ordinaryAccounts := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() {
			continue
		}
		if IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			cindyAccountIDs = append(cindyAccountIDs, account.ID)
			continue
		}
		ordinaryAccounts = append(ordinaryAccounts, *account)
	}
	if len(cindyAccountIDs) == 0 {
		return scope, nil
	}
	if len(ordinaryAccounts) == 0 {
		scope.CatalogOnly = true
		return scope, nil
	}

	sort.Slice(ordinaryAccounts, func(i, j int) bool {
		if ordinaryAccounts[i].Priority != ordinaryAccounts[j].Priority {
			return ordinaryAccounts[i].Priority < ordinaryAccounts[j].Priority
		}
		return ordinaryAccounts[i].ID < ordinaryAccounts[j].ID
	})
	ordinaryAccountIDs := make([]int64, 0, len(ordinaryAccounts))
	for i := range ordinaryAccounts {
		ordinaryAccountIDs = append(ordinaryAccountIDs, ordinaryAccounts[i].ID)
	}
	excludedAccountIDs := append([]int64(nil), cindyAccountIDs...)
	excludedAccountIDs = append(excludedAccountIDs, ordinaryAccountIDs[1:]...)
	sort.Slice(excludedAccountIDs, func(i, j int) bool { return excludedAccountIDs[i] < excludedAccountIDs[j] })
	scope.MergeCatalog = true
	scope.OrdinaryAccountIDs = ordinaryAccountIDs
	scope.ExcludedAccountIDs = excludedAccountIDs
	return scope, nil
}

// IsStrictCindyGroup remains for non-routing compatibility. Request routing
// must use ClassifyStrictCindyGroup so repository errors cannot fail open.
func (s *GatewayService) IsStrictCindyGroup(ctx context.Context, groupID *int64) bool {
	if s == nil {
		return false
	}
	strict, err := classifyStrictCindyGroup(ctx, s.accountRepo, groupID)
	return err == nil && strict
}

// IsStrictCindyGroup remains for non-routing compatibility. Request routing
// must use ClassifyStrictCindyGroup so repository errors cannot fail open.
func (s *OpenAIGatewayService) IsStrictCindyGroup(ctx context.Context, groupID *int64) bool {
	if s == nil {
		return false
	}
	strict, err := classifyStrictCindyGroup(ctx, s.accountRepo, groupID)
	return err == nil && strict
}
