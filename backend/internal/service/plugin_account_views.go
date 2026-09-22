package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const CindyAccountViewPluginKey = "codexrip.cindy-provider"
const CindyAccountViewID = "cindy-accounts"

var (
	ErrAccountViewInvalid              = infraerrors.BadRequest("ACCOUNT_VIEW_INVALID_REQUEST", "invalid account view context")
	ErrAccountViewUnavailable          = infraerrors.Conflict("ACCOUNT_VIEW_UNAVAILABLE", "account view is unavailable or has changed")
	ErrAccountViewScope                = infraerrors.Forbidden("ACCOUNT_VIEW_ACCOUNT_OUTSIDE_SCOPE", "an account is outside the current account view")
	ErrAccountViewUnsupportedOperation = infraerrors.BadRequest("ACCOUNT_VIEW_UNSUPPORTED_OPERATION", "account view operations must use a named host resource")
	accountViewIDPattern               = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	accountViewDigestPattern           = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func validateAccountViewPredicate(p extensionv1.AccountViewPredicate) error {
	for _, values := range [][]string{p.Platforms, p.Types, p.Statuses, p.Plans} {
		if err := boundedAccountViewStrings(values, 32); err != nil {
			return err
		}
	}
	if len(p.PrivacyMode) > 64 || strings.TrimSpace(p.PrivacyMode) != p.PrivacyMode ||
		(p.CindyBalanceStatus != "" && p.CindyBalanceStatus != "insufficient") ||
		(p.CindyHealthStatus != "" && p.CindyHealthStatus != "banned") {
		return ErrAccountViewInvalid
	}
	return nil
}

func boundedAccountViewStrings(values []string, limit int) error {
	if len(values) > limit {
		return ErrAccountViewInvalid
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || len(value) > 64 || strings.TrimSpace(value) != value || seen[value] || strings.ContainsAny(value, "\x00\r\n") {
			return ErrAccountViewInvalid
		}
		seen[value] = true
	}
	return nil
}

func accountViewLabelValid(label map[string]string) bool {
	if len(label) == 0 || len(label) > 16 {
		return false
	}
	for key, value := range label {
		if key == "" || len(key) > 32 || strings.TrimSpace(value) == "" || len([]rune(value)) > 256 {
			return false
		}
	}
	return true
}

func validateAccountViewContributions(manifest PluginManifest) error {
	byID := make(map[string]extensionv1.Contribution, len(manifest.Contributions))
	resourceActions := map[string]bool{}
	for _, contribution := range manifest.Contributions {
		if _, exists := byID[contribution.ID]; exists {
			return fmt.Errorf("duplicate contribution: %s", contribution.ID)
		}
		byID[contribution.ID] = contribution
	}
	for _, contribution := range manifest.Contributions {
		if contribution.AccountView != nil && contribution.Slot != extensionv1.AccountViewSlot {
			return ErrAccountViewInvalid
		}
		if len(contribution.ValueBindings) > 0 {
			if contribution.Slot != "account.columns" || contribution.Permission != "admin" || len(contribution.ValueBindings) > 16 {
				return ErrAccountViewInvalid
			}
			fields := make(map[string]bool, len(contribution.DisplayFields))
			for _, field := range contribution.DisplayFields {
				fields[field.Key] = true
			}
			for key, source := range contribution.ValueBindings {
				if !fields[key] || !slices.Contains([]string{"cindy_balance_probe_job_id", "cindy_balance_probe_outcome", "cindy_balance_probe_checked_at"}, source) {
					return ErrAccountViewInvalid
				}
			}
		}
		if action := contribution.ResourceAction; action != nil {
			if resourceActions[action.Resource] {
				return ErrAccountViewInvalid
			}
			resourceActions[action.Resource] = true
			if contribution.Slot != "account.actions" || contribution.Permission != "admin" || contribution.Capability == "" || contribution.Action != "" || contribution.Entrypoint != "" || action.Version != 1 || action.AccountParameter != "id" || action.AccountSource != "row.id" || action.Effect != "refresh_current_account_view" {
				return ErrAccountViewInvalid
			}
			declared := false
			for _, resource := range manifest.Resources {
				declared = declared || (resource.Name == action.Resource && resource.Permission == "admin" && resource.Capability == contribution.Capability)
			}
			if !declared {
				return ErrAccountViewInvalid
			}
			if action.RowPredicate != nil {
				if err := validateAccountViewPredicate(*action.RowPredicate); err != nil {
					return err
				}
			}
		}
		if contribution.Slot != extensionv1.AccountViewSlot {
			continue
		}
		view := contribution.AccountView
		if view == nil || !accountViewIDPattern.MatchString(contribution.ID) || contribution.Permission != "admin" || contribution.Capability == "" || contribution.AllAccounts || contribution.RetainedControls || contribution.Entrypoint != "" || contribution.Action != "" || contribution.ResourceAction != nil || len(contribution.Fields) != 0 || len(contribution.DisplayFields) != 0 || view.Version != 1 || view.Source != extensionv1.AccountConsoleSource {
			return ErrAccountViewInvalid
		}
		declaredAdmin := false
		for _, capability := range manifest.Capabilities {
			declaredAdmin = declaredAdmin || capability.ID == extensionv1.CapabilityAdmin
		}
		if !declaredAdmin || !accountViewLabelValid(contribution.Label) {
			return ErrAccountViewInvalid
		}
		if err := validateAccountViewPredicate(view.BaseQuery); err != nil {
			return err
		}
		if len(view.Presets) < 1 || len(view.Presets) > 16 || len(view.Layout) < 1 || len(view.Layout) > 16 {
			return ErrAccountViewInvalid
		}
		presets := make(map[string]bool, len(view.Presets))
		for _, preset := range view.Presets {
			if !accountViewIDPattern.MatchString(preset.ID) || presets[preset.ID] || !accountViewLabelValid(preset.Label) || !slices.Contains([]string{"cindy_total", "cindy_insufficient_count", "cindy_banned_count"}, preset.Counter) {
				return ErrAccountViewInvalid
			}
			if err := validateAccountViewPredicate(preset.Query); err != nil {
				return err
			}
			presets[preset.ID] = true
		}
		if !presets[view.DefaultPreset] {
			return ErrAccountViewInvalid
		}
		checkRef := func(ref, slot string) bool {
			item, exists := byID[ref]
			return accountViewIDPattern.MatchString(ref) && exists && item.Slot == slot && item.Permission == "admin"
		}
		if len(view.CoreFilterSurfaceRefs) > 4 || (len(view.CoreFilterSurfaceRefs) > 0 && !view.ExtendsCoreFilters) {
			return ErrAccountViewInvalid
		}
		coreSurfaces := map[string]bool{}
		for _, ref := range view.CoreFilterSurfaceRefs {
			if coreSurfaces[ref] || !checkRef(ref, "surface") {
				return ErrAccountViewInvalid
			}
			coreSurfaces[ref] = true
		}
		tables := 0
		surfaces := map[string]bool{}
		for _, node := range view.Layout {
			switch node.Kind {
			case "account_table":
				if node.SurfaceRef != "" {
					return ErrAccountViewInvalid
				}
				tables++
			case "surface":
				if !checkRef(node.SurfaceRef, "surface") || surfaces[node.SurfaceRef] {
					return ErrAccountViewInvalid
				}
				surfaces[node.SurfaceRef] = true
			default:
				return ErrAccountViewInvalid
			}
		}
		if tables != 1 || len(view.Table.Layouts) == 0 || len(view.Table.Layouts) > 3 || view.Table.ColumnRefs == nil || view.Table.ActionRefs == nil {
			return ErrAccountViewInvalid
		}
		layouts := map[string]bool{}
		for _, layout := range view.Table.Layouts {
			if layouts[layout] || !slices.Contains([]string{"table", "compact", "cards"}, layout) {
				return ErrAccountViewInvalid
			}
			layouts[layout] = true
		}
		for slot, refs := range map[string][]string{"account.columns": view.Table.ColumnRefs, "account.actions": view.Table.ActionRefs} {
			seen := map[string]bool{}
			if len(refs) > 32 {
				return ErrAccountViewInvalid
			}
			for _, ref := range refs {
				if seen[ref] || !checkRef(ref, slot) {
					return ErrAccountViewInvalid
				}
				seen[ref] = true
			}
		}
		if nav := view.Navigation; nav != nil {
			if nav.Section != "admin.extensions" || nav.TargetView != contribution.ID || nav.Icon != "accounts" || nav.Order < -10000 || nav.Order > 10000 {
				return ErrAccountViewInvalid
			}
		}
		if len(view.LegacyQueryAliases) > 8 || (len(view.LegacyQueryAliases) > 0 && (manifest.ID != CindyAccountViewPluginKey || contribution.ID != CindyAccountViewID)) {
			return ErrAccountViewInvalid
		}
		priorities, matches := map[int]bool{}, map[string]bool{}
		for _, alias := range view.LegacyQueryAliases {
			if !presets[alias.Preset] || len(alias.Match) == 0 || len(alias.Match) > 3 || alias.Priority < 0 || alias.Priority > 1000 || priorities[alias.Priority] {
				return ErrAccountViewInvalid
			}
			for key, value := range alias.Match {
				if map[string]string{"cindy_only": "true", "cindy_balance_status": "insufficient", "cindy_health_status": "banned"}[key] != value || value == "" {
					return ErrAccountViewInvalid
				}
			}
			raw, _ := json.Marshal(alias.Match)
			if matches[string(raw)] {
				return ErrAccountViewInvalid
			}
			matches[string(raw)], priorities[alias.Priority] = true, true
		}
	}
	return nil
}

func AccountViewDefinitionDigest(contribution *extensionv1.Contribution) string {
	if contribution == nil || contribution.AccountView == nil {
		return ""
	}
	raw, err := json.Marshal(contribution)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateAccountViewRegistry(installations []*PluginInstallation) error {
	owners := map[string]int64{}
	for _, installation := range installations {
		if installation == nil {
			continue
		}
		for _, contribution := range installation.Manifest.Contributions {
			if contribution.Slot != extensionv1.AccountViewSlot {
				continue
			}
			key := installation.PluginKey + "/" + contribution.ID
			if previous, exists := owners[key]; exists && previous != installation.ID {
				return ErrAccountViewUnavailable
			}
			owners[key] = installation.ID
		}
	}
	return nil
}

type accountViewContextKey struct{}
type retainedAccountViewKey struct{}

// BoundAccountView is host-private. The primary PluginExecution is deliberately
// separate: an account-tools action may originate in a Cindy-owned view.
type BoundAccountView struct {
	Request      extensionv1.AccountViewContextV1
	Execution    PluginExecution
	manager      *PluginManager
	installation *PluginInstallation
	contribution extensionv1.Contribution
	preset       extensionv1.AccountViewPreset
}

func AccountViewFromContext(ctx context.Context) (*BoundAccountView, bool) {
	if ctx == nil {
		return nil, false
	}
	view, ok := ctx.Value(accountViewContextKey{}).(*BoundAccountView)
	return view, ok && view != nil
}

func RetainedAccountViewIdentity(ctx context.Context) (*extensionv1.AccountViewIdentityV1, bool) {
	identity, ok := ctx.Value(retainedAccountViewKey{}).(extensionv1.AccountViewIdentityV1)
	if !ok {
		return nil, false
	}
	return &identity, true
}

func AccountViewExecutionFromContext(ctx context.Context) (PluginExecution, string, bool) {
	view, ok := AccountViewFromContext(ctx)
	if !ok {
		return PluginExecution{}, "", false
	}
	return view.Execution, view.Request.PluginKey, true
}

func ValidateAccountViewIdentity(identity extensionv1.AccountViewIdentityV1) error {
	if identity.Version != 1 || identity.PluginID <= 0 || !pluginIDPattern.MatchString(identity.PluginKey) || len(identity.PluginKey) > 160 || !accountViewIDPattern.MatchString(identity.ViewID) || !accountViewIDPattern.MatchString(identity.PresetID) || !accountViewDigestPattern.MatchString(identity.PackageSHA256) || !accountViewDigestPattern.MatchString(identity.ViewDefinitionDigest) {
		return ErrAccountViewInvalid
	}
	return nil
}

// Only a host-registered retained route may use this identity-only context. It
// cannot be mistaken for a business BoundAccountView or authorize a new job.
func (m *PluginManager) BindRetainedAccountView(ctx context.Context, request extensionv1.AccountViewContextV1) (context.Context, error) {
	if err := ValidateAccountViewIdentity(request.AccountViewIdentityV1); err != nil {
		return nil, err
	}
	if m == nil || m.repo == nil {
		return nil, ErrAccountViewUnavailable
	}
	current, err := m.repo.GetByID(ctx, request.PluginID)
	if err != nil || current == nil || current.PluginKey != request.PluginKey || current.Manifest.ID != request.PluginKey || current.PackageSHA256 != request.PackageSHA256 {
		return nil, ErrAccountViewUnavailable
	}
	found := false
	for _, contribution := range current.Manifest.Contributions {
		if contribution.ID != request.ViewID || contribution.Slot != extensionv1.AccountViewSlot {
			continue
		}
		if found || AccountViewDefinitionDigest(&contribution) != request.ViewDefinitionDigest {
			return nil, ErrAccountViewUnavailable
		}
		for _, preset := range contribution.AccountView.Presets {
			if preset.ID == request.PresetID {
				found = true
			}
		}
	}
	if !found {
		return nil, ErrAccountViewUnavailable
	}
	return context.WithValue(ctx, retainedAccountViewKey{}, request.AccountViewIdentityV1), nil
}

func NormalizeAccountViewQuery(query extensionv1.AccountViewQueryV1) (extensionv1.AccountViewQueryV1, error) {
	for _, values := range [][]string{query.Platforms, query.Types, query.Statuses, query.Plans} {
		if err := boundedAccountViewStrings(values, 32); err != nil {
			return query, err
		}
	}
	if len(query.Search) > 100 || len(query.PrivacyMode) > 64 || query.GroupID < AccountListGroupUngrouped {
		return query, ErrAccountViewInvalid
	}
	query.Search = strings.TrimSpace(query.Search)
	if query.SortBy != "" && !slices.Contains([]string{"id", "name", "platform", "type", "status", "schedulable", "priority", "concurrency", "rate_multiplier", "upstream_billing_rate", "last_used_at", "created_at", "updated_at", "expires_at"}, query.SortBy) {
		return query, ErrAccountViewInvalid
	}
	if query.SortOrder != "" && query.SortOrder != "asc" && query.SortOrder != "desc" {
		return query, ErrAccountViewInvalid
	}
	for _, values := range [][]int64{query.Tags, query.AccountIDs} {
		if len(values) > 3200 {
			return query, ErrAccountViewInvalid
		}
		for _, id := range values {
			if id <= 0 {
				return query, ErrAccountViewInvalid
			}
		}
	}
	for sentinel, values := range map[string][]string{"direct": query.Proxies, "uncategorized": query.Folders} {
		if len(values) > 3200 {
			return query, ErrAccountViewInvalid
		}
		for _, value := range values {
			if value == sentinel {
				continue
			}
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
				return query, ErrAccountViewInvalid
			}
		}
	}
	normalizeStrings := func(values []string) []string {
		out := slices.Clone(values)
		slices.Sort(out)
		return slices.Compact(out)
	}
	normalizeIDs := func(values []int64) []int64 {
		out := slices.Clone(values)
		slices.Sort(out)
		return slices.Compact(out)
	}
	query.Platforms, query.Types, query.Statuses, query.Plans = normalizeStrings(query.Platforms), normalizeStrings(query.Types), normalizeStrings(query.Statuses), normalizeStrings(query.Plans)
	query.Proxies, query.Folders = normalizeStrings(query.Proxies), normalizeStrings(query.Folders)
	query.Tags, query.AccountIDs = normalizeIDs(query.Tags), normalizeIDs(query.AccountIDs)
	return query, nil
}

func AccountViewQueryFilters(query extensionv1.AccountViewQueryV1) AccountConsoleFilters {
	filters := AccountConsoleFilters{Platforms: query.Platforms, Types: query.Types, Statuses: query.Statuses, Plans: query.Plans, TagIDs: query.Tags, AccountIDs: query.AccountIDs, GroupID: query.GroupID, PrivacyMode: query.PrivacyMode, Search: query.Search, SortBy: query.SortBy, SortOrder: query.SortOrder}
	for _, value := range query.Proxies {
		if value == "direct" {
			filters.IncludeDirect = true
		} else if id, err := strconv.ParseInt(value, 10, 64); err == nil {
			filters.ProxyIDs = append(filters.ProxyIDs, id)
		}
	}
	for _, value := range query.Folders {
		if value == "uncategorized" {
			filters.IncludeUncategorized = true
		} else if id, err := strconv.ParseInt(value, 10, 64); err == nil {
			filters.FolderIDs = append(filters.FolderIDs, id)
		}
	}
	return filters
}

func (m *PluginManager) BindAccountViewRequest(ctx context.Context, request extensionv1.AccountViewContextV1) (context.Context, func(), error) {
	if err := ValidateAccountViewIdentity(request.AccountViewIdentityV1); err != nil {
		return nil, nil, err
	}
	query, err := NormalizeAccountViewQuery(request.Query)
	if err != nil {
		return nil, nil, err
	}
	request.Query = query
	if m == nil || m.repo == nil {
		return nil, nil, ErrAccountViewUnavailable
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return nil, nil, ErrAccountViewUnavailable
	}
	current, err := m.repo.GetByID(ctx, request.PluginID)
	if err != nil || current == nil || current.State != PluginStateEnabled || current.PluginKey != request.PluginKey || current.Manifest.ID != request.PluginKey || current.PackageSHA256 != request.PackageSHA256 || !samePluginRuntime(current, registry.installations[current.ID]) {
		return nil, nil, ErrAccountViewUnavailable
	}
	all := make([]*PluginInstallation, 0, len(registry.installations))
	for _, installation := range registry.installations {
		all = append(all, installation)
	}
	if err := validateAccountViewRegistry(all); err != nil {
		return nil, nil, err
	}
	var selected *extensionv1.Contribution
	for _, contribution := range current.Manifest.Contributions {
		if contribution.ID == request.ViewID && contribution.Slot == extensionv1.AccountViewSlot {
			if selected != nil {
				return nil, nil, ErrAccountViewUnavailable
			}
			copy := contribution
			selected = &copy
		}
	}
	if selected == nil || selected.Permission != "admin" || selected.AccountView == nil || AccountViewDefinitionDigest(selected) != request.ViewDefinitionDigest || !pluginContributionBindingsEnabled(current, selected) {
		return nil, nil, ErrAccountViewUnavailable
	}
	if err := validateAccountViewContributions(current.Manifest); err != nil {
		return nil, nil, ErrAccountViewUnavailable
	}
	var preset *extensionv1.AccountViewPreset
	for _, item := range selected.AccountView.Presets {
		if item.ID == request.PresetID {
			copy := item
			preset = &copy
		}
	}
	if preset == nil {
		return nil, nil, ErrAccountViewInvalid
	}
	runtime := registry.runtimes[current.ID]
	if runtime == nil || runtime.client == nil || runtime.client.Exited() || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) {
		return nil, nil, ErrAccountViewUnavailable
	}
	if _, applied := m.contributionAppliedRevision(current, runtime); !applied {
		return nil, nil, ErrAccountViewUnavailable
	}
	if enabled, known := m.contributionConfigured(current, runtime, selected.ConfigFlag); !known || !enabled {
		return nil, nil, ErrAccountViewUnavailable
	}
	bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
	if err != nil {
		return nil, nil, ErrAccountViewUnavailable
	}
	view := &BoundAccountView{Request: request, Execution: PluginExecution{current.ID, current.RuntimeGeneration}, manager: m, installation: current, contribution: *selected, preset: *preset}
	return context.WithValue(bound, accountViewContextKey{}, view), release, nil
}

func accountMatchesViewPredicate(account *Account, predicate extensionv1.AccountViewPredicate) bool {
	if account == nil || account.ID <= 0 {
		return false
	}
	facts := AccountViewFactsFromAccount(account, time.Now())
	if predicate.CindyOnly != nil && *predicate.CindyOnly != facts.CanonicalCindy {
		return false
	}
	if len(predicate.Platforms) > 0 && !slices.Contains(predicate.Platforms, account.Platform) {
		return false
	}
	if len(predicate.Types) > 0 && !slices.Contains(predicate.Types, account.Type) {
		return false
	}
	if len(predicate.Statuses) > 0 && !slices.Contains(predicate.Statuses, facts.Status) {
		return false
	}
	if len(predicate.Plans) > 0 && !slices.ContainsFunc(predicate.Plans, func(plan string) bool { return strings.EqualFold(plan, facts.Plan) }) {
		return false
	}
	if predicate.PrivacyMode != "" {
		privacy := facts.PrivacyMode
		if predicate.PrivacyMode == AccountPrivacyModeUnsetFilter {
			if privacy != "" {
				return false
			}
		} else if privacy != predicate.PrivacyMode {
			return false
		}
	}
	return (predicate.CindyBalanceStatus == "" || (facts.CanonicalCindy && facts.CindyBalanceInsufficient)) && (predicate.CindyHealthStatus == "" || (facts.CanonicalCindy && facts.CindyBanned))
}

// Native host row facts are a scalar projection, not a plugin Account DTO.
// The declaration matcher and all native layouts consume exactly these values.
type AccountViewFactsV1 struct {
	Version                  int    `json:"version"`
	Status                   string `json:"status"`
	Plan                     string `json:"plan"`
	PrivacyMode              string `json:"privacy_mode"`
	CanonicalCindy           bool   `json:"canonical_cindy"`
	CindyBalanceInsufficient bool   `json:"cindy_balance_insufficient"`
	CindyBanned              bool   `json:"cindy_banned"`
}

func AccountViewFactsFromAccount(account *Account, now time.Time) *AccountViewFactsV1 {
	if account == nil {
		return nil
	}
	privacy, _ := account.Extra["privacy_mode"].(string)
	return &AccountViewFactsV1{Version: 1, Status: accountConsoleStatus(account, now), Plan: accountConsolePlan(account), PrivacyMode: privacy, CanonicalCindy: accountViewCanonicalCindy(account), CindyBalanceInsufficient: account.CindyBalanceInsufficientAt != nil, CindyBanned: account.CindyBannedAt != nil}
}

func accountViewCanonicalCindy(account *Account) bool {
	return account != nil && account.Platform == PlatformCindy && account.WirePlatform == WirePlatformOpenAI && account.ProviderProfile == ProviderProfileCindyLaxaV1 && IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}

func (view *BoundAccountView) allows(account *Account) bool {
	return view != nil && account != nil && accountMatchesViewPredicate(account, view.contribution.AccountView.BaseQuery) && accountMatchesViewPredicate(account, view.preset.Query) && contributionAccountAllowed(view.installation, &view.contribution, *extensionAccount(account))
}

func filterAccountViewAccounts(ctx context.Context, accounts []*Account) ([]*Account, error) {
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		return accounts, nil
	}
	if err := ValidateAccountViewFresh(ctx); err != nil {
		return nil, err
	}
	out := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if view.allows(account) {
			out = append(out, account)
		}
	}
	return out, nil
}

type accountViewAccountDirectory interface {
	ReadAccountViewAccount(context.Context, int64) (*Account, error)
}

// This internal directory deliberately never exposes its native Account to the
// extension protocol. It is used only to validate real selected IDs in the host.
func (s *OpenAIGatewayService) ReadAccountViewAccount(ctx context.Context, id int64) (*Account, error) {
	return s.accountRepo.GetByID(ctx, id)
}

func ValidateAccountViewTargets(ctx context.Context, ids []int64) error {
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		return nil
	}
	if err := ValidateAccountViewFresh(ctx); err != nil {
		return err
	}
	directory, ok := view.manager.accountDirectory.(accountViewAccountDirectory)
	if !ok {
		return ErrAccountViewUnavailable
	}
	for _, id := range ids {
		if id <= 0 {
			return ErrAccountViewScope
		}
		if len(view.Request.Query.AccountIDs) > 0 && !slices.Contains(view.Request.Query.AccountIDs, id) {
			return ErrAccountViewScope
		}
		account, err := directory.ReadAccountViewAccount(ctx, id)
		if err != nil || account == nil || account.ID != id || !view.allows(account) {
			return ErrAccountViewScope
		}
	}
	return nil
}

func accountViewPresetContext(ctx context.Context, preset extensionv1.AccountViewPreset) context.Context {
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		return ctx
	}
	copy := *view
	copy.preset = preset
	return context.WithValue(ctx, accountViewContextKey{}, &copy)
}

func accountViewCounter(name string, accounts []*Account) int {
	count := 0
	for _, account := range accounts {
		if !hasCanonicalCindyProviderIdentity(account) {
			continue
		}
		if name == "cindy_total" || (name == "cindy_insufficient_count" && account.CindyBalanceInsufficientAt != nil) || (name == "cindy_banned_count" && account.CindyBannedAt != nil) {
			count++
		}
	}
	return count
}

func accountViewJSONEqual(left, right any) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return bytes.Equal(a, b)
}

func ValidateAccountViewPredicateQuery(ctx context.Context, supplied map[string]string) error {
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		return nil
	}
	expected := map[string]string{}
	for _, predicate := range []extensionv1.AccountViewPredicate{view.contribution.AccountView.BaseQuery, view.preset.Query} {
		if predicate.CindyOnly != nil {
			expected["cindy_only"] = strconv.FormatBool(*predicate.CindyOnly)
		}
		if predicate.CindyBalanceStatus != "" {
			expected["cindy_balance_status"] = predicate.CindyBalanceStatus
		}
		if predicate.CindyHealthStatus != "" {
			expected["cindy_health_status"] = predicate.CindyHealthStatus
		}
	}
	for key, value := range supplied {
		if expected[key] == "" || expected[key] != value {
			return ErrAccountViewInvalid
		}
	}
	return nil
}

func ValidateAccountViewSelection(ctx context.Context, ids []int64) error {
	if err := ValidateAccountViewTargets(ctx, ids); err != nil {
		return err
	}
	return ValidateBoundResourceTargets(ctx, ids)
}

func ValidateAccountViewFresh(ctx context.Context) error {
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := view.manager.repo.GetByID(ctx, view.Execution.ID)
	if err != nil || current == nil || current.State != PluginStateEnabled || current.Revision != view.installation.Revision || current.ConfigEncrypted != view.installation.ConfigEncrypted || !samePluginRuntime(current, view.installation) {
		return ErrAccountViewUnavailable
	}
	return nil
}

// Shared row locks are acquired in ascending installation ID order for both
// action and view fences. A->B and B->A operations therefore use one lock order.
type PluginExecutionFence struct {
	OriginCreate           bool
	CreateContributionID   string
	CreateDefinitionSHA256 string
	ConfigSHA256           string
	ID                     int64
	Generation             int64
	PluginKey              string
	PackageSHA256          string
	PolicyRevision         int64
	OriginView             bool
	Primary                bool
}

func mergePluginExecutionFences(fences []PluginExecutionFence) ([]PluginExecutionFence, error) {
	slices.SortFunc(fences, func(a, b PluginExecutionFence) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	out := make([]PluginExecutionFence, 0, len(fences))
	for _, fence := range fences {
		if fence.ID <= 0 || fence.Generation <= 0 {
			return nil, ErrAccountViewUnavailable
		}
		if len(out) > 0 && out[len(out)-1].ID == fence.ID {
			previous := &out[len(out)-1]
			if previous.Generation != fence.Generation || (previous.PluginKey != "" && fence.PluginKey != "" && previous.PluginKey != fence.PluginKey) {
				return nil, ErrAccountViewUnavailable
			}
			previousStrict, nextStrict := previous.OriginView || previous.OriginCreate, fence.OriginView || fence.OriginCreate
			if previousStrict && nextStrict && (previous.PackageSHA256 != fence.PackageSHA256 || previous.PolicyRevision != fence.PolicyRevision) {
				return nil, ErrAccountViewUnavailable
			}
			if previous.ConfigSHA256 != "" && fence.ConfigSHA256 != "" && previous.ConfigSHA256 != fence.ConfigSHA256 {
				return nil, ErrAccountViewUnavailable
			}
			if previous.OriginCreate && fence.OriginCreate && (previous.CreateContributionID != fence.CreateContributionID || previous.CreateDefinitionSHA256 != fence.CreateDefinitionSHA256 || previous.ConfigSHA256 != fence.ConfigSHA256) {
				return nil, ErrAccountViewUnavailable
			}
			if previous.PluginKey == "" {
				previous.PluginKey = fence.PluginKey
			}
			if previous.ConfigSHA256 == "" {
				previous.ConfigSHA256 = fence.ConfigSHA256
			}
			if nextStrict {
				previous.PackageSHA256, previous.PolicyRevision = fence.PackageSHA256, fence.PolicyRevision
			}
			if fence.OriginCreate {
				previous.CreateContributionID, previous.CreateDefinitionSHA256, previous.ConfigSHA256 = fence.CreateContributionID, fence.CreateDefinitionSHA256, fence.ConfigSHA256
			}
			previous.Primary = previous.Primary || fence.Primary
			previous.OriginView = previous.OriginView || fence.OriginView
			previous.OriginCreate = previous.OriginCreate || fence.OriginCreate
			continue
		}
		out = append(out, fence)
	}
	return out, nil
}

func PluginExecutionFences(ctx context.Context, primaryKey string) ([]PluginExecutionFence, error) {
	var fences []PluginExecutionFence
	if create, bound := AccountCreateFromContext(ctx); bound && primaryKey == "" {
		primaryKey = create.PrimaryPluginKey
	}
	if execution, bound := PluginExecutionFromContext(ctx); bound {
		fences = append(fences, PluginExecutionFence{ID: execution.ID, Generation: execution.Generation, PluginKey: primaryKey, Primary: true})
	}
	if view, bound := AccountViewFromContext(ctx); bound {
		fences = append(fences, PluginExecutionFence{ID: view.Execution.ID, Generation: view.Execution.Generation, PluginKey: view.Request.PluginKey, PackageSHA256: view.Request.PackageSHA256, PolicyRevision: view.installation.Revision, ConfigSHA256: accountCreateConfigDigest(view.installation.ConfigEncrypted), OriginView: true})
	}
	if create, bound := AccountCreateFromContext(ctx); bound {
		fences = append(fences, PluginExecutionFence{ID: create.Execution.ID, Generation: create.Execution.Generation,
			PluginKey: create.installation.PluginKey, PackageSHA256: create.installation.PackageSHA256, PolicyRevision: create.installation.Revision,
			OriginCreate: true, CreateContributionID: create.contribution.ID, CreateDefinitionSHA256: AccountCreateDefinitionDigest(&create.contribution),
			ConfigSHA256: accountCreateConfigDigest(create.installation.ConfigEncrypted)})
	}
	return mergePluginExecutionFences(fences)
}

type pluginUIActorKey struct{}

func WithPluginUIActor(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, pluginUIActorKey{}, id)
}

// The encrypted UI asset token is reused as a host-only request binding. It is
// not administrator authentication: the normal admin middleware still applies.
func (m *PluginManager) ValidateUIRequestBinding(ctx context.Context, token string, pluginID, actorID int64, identity *extensionv1.AccountViewIdentityV1) error {
	claims, err := m.parseUIAssetToken(token)
	if err != nil || claims.Permission != "admin" || claims.PluginID != pluginID || claims.ActorID <= 0 || claims.ActorID != actorID {
		return ErrAccountViewUnavailable
	}
	if (claims.ViewContext == nil) != (identity == nil) || (identity != nil && *claims.ViewContext != *identity) {
		return ErrAccountViewUnavailable
	}
	installation, err := m.repo.GetByID(ctx, pluginID)
	if err != nil || installation == nil || installation.PackageSHA256 != claims.PackageSHA256 {
		return ErrAccountViewUnavailable
	}
	return nil
}
