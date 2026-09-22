package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var (
	ErrAccountEditInvalid            = infraerrors.BadRequest("ACCOUNT_EDIT_INVALID_REQUEST", "invalid provider edit input")
	ErrAccountEditUnavailable        = infraerrors.Conflict("ACCOUNT_EDIT_UNAVAILABLE", "provider editing is unavailable or has changed")
	ErrAccountEditStateChanged       = infraerrors.Conflict("ACCOUNT_EDIT_STATE_CHANGED", "account edit state has changed")
	ErrAccountEditCatalogUnavailable = infraerrors.Conflict("ACCOUNT_EDIT_CATALOG_UNAVAILABLE", "provider edit catalog is unavailable or has changed")
)

func validateAccountEditContributions(manifest PluginManifest) error {
	seen := false
	for _, contribution := range manifest.Contributions {
		definition := contribution.AccountEdit
		if contribution.Slot != extensionv1.AccountEditSlot {
			if definition != nil {
				return ErrAccountEditInvalid
			}
			continue
		}
		if definition == nil || seen || manifest.ID != CindyAccountViewPluginKey || !accountViewIDPattern.MatchString(contribution.ID) ||
			contribution.Capability != extensionv1.CapabilityProvider || contribution.Permission != "admin" || !accountViewLabelValid(contribution.Label) ||
			contribution.Entrypoint != "" || contribution.Action != "" || contribution.AccountFilter != nil || contribution.AccountView != nil || contribution.AccountCreate != nil ||
			contribution.ResourceAction != nil || contribution.AllAccounts || contribution.RetainedControls || len(contribution.Fields) != 0 || len(contribution.Assets) != 0 ||
			len(contribution.DisplayFields) != 0 || len(contribution.ValueBindings) != 0 || len(contribution.Events) != 0 {
			return ErrAccountEditInvalid
		}
		seen = true
		admin := false
		for _, capability := range manifest.Capabilities {
			admin = admin || capability.ID == extensionv1.CapabilityAdmin
		}
		if !admin || definition.Version != 1 || definition.Platform != PlatformCindy || definition.AccountType != AccountTypeAPIKey || definition.CredentialProfile != ProviderProfileCindyLaxaV1 ||
			definition.CredentialUI.BaseURL != "https://api.laxarouter.ai" || !definition.CredentialUI.BaseURLReadonly || !accountViewLabelValid(definition.CredentialUI.Hint) ||
			definition.CatalogSource != "provider_catalog_snapshot_v1" || definition.MappingPolicy != "managed_catalog_v1" || definition.UnlistedFields != "preserve" || len(definition.WireControls) > 3 ||
			!accountViewLabelValid(definition.Labels.ManagedCatalog) || !accountViewLabelValid(definition.Labels.ManagedAliases) || !accountViewLabelValid(definition.Labels.CustomMappings) {
			return ErrAccountEditInvalid
		}
		targets := map[string]bool{}
		for _, control := range definition.WireControls {
			allowed, valid := accountEditModeValues[control.Target]
			if !valid || targets[control.Target] || len(control.Values) == 0 || len(control.Values) > len(allowed) {
				return ErrAccountEditInvalid
			}
			targets[control.Target] = true
			values := map[string]bool{}
			for _, value := range control.Values {
				if !slices.Contains(allowed, value) || values[value] {
					return ErrAccountEditInvalid
				}
				values[value] = true
			}
		}
	}
	return nil
}

func AccountEditDefinitionDigest(contribution *extensionv1.Contribution) string {
	if contribution == nil || contribution.AccountEdit == nil {
		return ""
	}
	raw, err := json.Marshal(contribution)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateAccountEditRegistry(installations []*PluginInstallation) error {
	owner := int64(0)
	for _, installation := range installations {
		if installation == nil {
			continue
		}
		for _, contribution := range installation.Manifest.Contributions {
			if contribution.Slot == extensionv1.AccountEditSlot && contribution.AccountEdit != nil {
				if owner != 0 {
					return ErrAccountEditUnavailable
				}
				owner = installation.ID
			}
		}
	}
	return nil
}

func accountEditContribution(installation *PluginInstallation, id string) *extensionv1.Contribution {
	if installation == nil {
		return nil
	}
	for _, contribution := range installation.Manifest.Contributions {
		if contribution.ID == id && contribution.Slot == extensionv1.AccountEditSlot && contribution.AccountEdit != nil {
			copy := contribution
			return &copy
		}
	}
	return nil
}

// Resolve from persisted identity/registry, including disabled owners. This
// read does not acquire a runtime/business lease: deliberate typed no-ops still
// reject stale references without depending on process health.
func (m *PluginManager) resolveAccountEdit(ctx context.Context, account *Account) (*PluginInstallation, *extensionv1.Contribution, error) {
	if m == nil || m.repo == nil || !hasCanonicalCindyProviderIdentity(account) || account.ID <= 0 {
		return nil, nil, ErrAccountEditUnavailable
	}
	installations, err := m.repo.List(ctx)
	if err != nil || validateAccountEditRegistry(installations) != nil {
		return nil, nil, ErrAccountEditUnavailable
	}
	for _, installation := range installations {
		if installation == nil || installation.PluginKey != CindyAccountViewPluginKey || installation.Manifest.ID != installation.PluginKey {
			continue
		}
		if validateAccountEditContributions(installation.Manifest) != nil {
			return nil, nil, ErrAccountEditUnavailable
		}
		for _, contribution := range installation.Manifest.Contributions {
			if contribution.Slot == extensionv1.AccountEditSlot && contribution.AccountEdit != nil && contribution.AccountEdit.CredentialProfile == account.EffectiveProviderProfile() {
				frozen := *installation
				frozen.Bindings = slices.Clone(installation.Bindings)
				raw, err := json.Marshal(installation.Manifest)
				if err != nil {
					return nil, nil, ErrAccountEditUnavailable
				}
				var manifest PluginManifest
				if json.Unmarshal(raw, &manifest) != nil {
					return nil, nil, ErrAccountEditUnavailable
				}
				frozen.Manifest = manifest
				return &frozen, accountEditContribution(&frozen, contribution.ID), nil
			}
		}
	}
	return nil, nil, ErrAccountEditUnavailable
}

func (m *PluginManager) accountEditReady(current *PluginInstallation, contribution *extensionv1.Contribution, account *Account) (*pluginRuntime, bool) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" || current == nil || current.State != PluginStateEnabled || current.RuntimeGeneration <= 0 ||
		!samePluginRuntime(current, registry.installations[current.ID]) || contribution == nil || validateAccountEditContributions(current.Manifest) != nil {
		return nil, false
	}
	if account != nil && (!hasCanonicalCindyProviderIdentity(account) || !contributionAccountAllowed(current, contribution, extensionv1.Account{ID: account.ID, Platform: account.Platform, Type: account.Type})) {
		return nil, false
	}
	runtime := registry.runtimes[current.ID]
	if runtime == nil || runtime.client == nil || runtime.draining.Load() || runtime.client.Exited() || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) {
		return nil, false
	}
	if _, applied := m.contributionAppliedRevision(current, runtime); !applied {
		return nil, false
	}
	if enabled, known := m.contributionConfigured(current, runtime, contribution.ConfigFlag); !enabled || !known {
		return nil, false
	}
	return runtime, true
}

type AccountEditProfileReference struct {
	PluginID          int64  `json:"plugin_id,omitempty"`
	PluginKey         string `json:"plugin_key,omitempty"`
	ContributionID    string `json:"contribution_id,omitempty"`
	PackageSHA256     string `json:"package_sha256,omitempty"`
	DefinitionSHA256  string `json:"definition_sha256,omitempty"`
	RuntimeGeneration int64  `json:"runtime_generation,omitempty"`
	Available         bool   `json:"available"`
	Reason            string `json:"reason,omitempty"`
}

type AccountEditFieldState struct {
	Present    bool            `json:"present"`
	Recognized bool            `json:"recognized"`
	Value      json.RawMessage `json:"value,omitempty"`
	Effective  string          `json:"effective"`
}

type AccountEditCatalogModel struct {
	extensionv1.CindyCatalogModel
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
}

type AccountEditCatalog struct {
	Status    string                     `json:"status"`
	Reason    string                     `json:"reason,omitempty"`
	Namespace string                     `json:"namespace,omitempty"`
	Models    *[]AccountEditCatalogModel `json:"models,omitempty"`
	Aliases   *map[string]string         `json:"aliases,omitempty"`
}

type AccountEditContext struct {
	SchemaVersion   int                              `json:"schema_version"`
	Kind            string                           `json:"kind"`
	AccountID       int64                            `json:"account_id"`
	Profile         *AccountEditProfileReference     `json:"profile,omitempty"`
	EditStateSHA256 string                           `json:"edit_state_sha256,omitempty"`
	Values          map[string]AccountEditFieldState `json:"values,omitempty"`
	Catalog         *AccountEditCatalog              `json:"catalog,omitempty"`
}

func accountEditFieldState(account *Account, target, effective string) AccountEditFieldState {
	state := AccountEditFieldState{Effective: effective}
	for index, key := range accountEditTargetKeys(target) {
		raw, present := account.Extra[key]
		if !present {
			continue
		}
		state.Present = true
		if index > 0 { // legacy WS fallback bool; its native encoding remains stored
			if _, ok := raw.(bool); ok {
				state.Recognized = true
				state.Value, _ = json.Marshal(effective)
				return state
			}
			continue
		}
		if raw == nil {
			state.Recognized = true
			state.Value = json.RawMessage("null")
			return state
		}
		if value, ok := raw.(string); ok && (slices.Contains(accountEditModeValues[target], value) || (target == "responses_websocket_mode" && slices.Contains([]string{"shared", "dedicated"}, value))) {
			state.Recognized = true
			state.Value, _ = json.Marshal(value)
		}
		return state
	}
	return state
}

func newAccountEditContext(account *Account, wsDefault string) *AccountEditContext {
	result := &AccountEditContext{SchemaVersion: 1, Kind: "core", AccountID: account.ID}
	if !hasCanonicalCindyProviderIdentity(account) {
		return result
	}
	result.Kind, result.EditStateSHA256 = "provider", AccountEditStateDigest(account)
	result.Profile = &AccountEditProfileReference{Reason: "account_edit_unavailable"}
	result.Catalog = &AccountEditCatalog{Status: "unavailable", Reason: "profile_unavailable"}
	responses, _ := account.Extra["openai_responses_mode"].(string)
	result.Values = map[string]AccountEditFieldState{
		"responses_mode":           accountEditFieldState(account, "responses_mode", string(openai_compat.NormalizeResponsesSupportMode(responses))),
		"compact_mode":             accountEditFieldState(account, "compact_mode", account.GetOpenAICompactMode()),
		"responses_websocket_mode": accountEditFieldState(account, "responses_websocket_mode", account.ResolveOpenAIResponsesWebSocketV2Mode(wsDefault)),
	}
	return result
}

func accountEditProfile(current *PluginInstallation, contribution *extensionv1.Contribution) *AccountEditProfileReference {
	return &AccountEditProfileReference{PluginID: current.ID, PluginKey: current.PluginKey, ContributionID: contribution.ID,
		PackageSHA256: current.PackageSHA256, DefinitionSHA256: AccountEditDefinitionDigest(contribution), RuntimeGeneration: current.RuntimeGeneration}
}

func accountEditCatalog(snapshot *CindyCatalogSnapshot) *AccountEditCatalog {
	if snapshot == nil {
		return &AccountEditCatalog{Status: "unavailable", Reason: "catalog_rpc_failed"}
	}
	if !snapshot.Config.CatalogEnabled {
		return &AccountEditCatalog{Status: "unavailable", Reason: "catalog_disabled"}
	}
	models := make([]AccountEditCatalogModel, 0, len(snapshot.CatalogModels))
	public := map[string]bool{}
	for _, model := range snapshot.CatalogModels {
		models = append(models, AccountEditCatalogModel{CindyCatalogModel: model, Type: "model"})
		public[model.ID] = model.PublicModel
	}
	aliases := map[string]string{}
	for alias, target := range snapshot.CompatibilityAliases {
		if public[target] {
			aliases[alias] = target
		}
	}
	return &AccountEditCatalog{Status: "ready", Namespace: snapshot.Namespace, Models: &models, Aliases: &aliases}
}

func (m *PluginManager) AccountEditContext(ctx context.Context, account *Account) (*AccountEditContext, error) {
	if account == nil || account.ID <= 0 {
		return nil, ErrAccountEditInvalid
	}
	if err := ValidateAccountViewTargets(ctx, []int64{account.ID}); err != nil {
		return nil, err
	}
	defaultMode := "ctx_pool"
	if m != nil && m.cfg != nil {
		defaultMode = m.cfg.Gateway.OpenAIWS.IngressModeDefault
	}
	result := newAccountEditContext(account, defaultMode)
	if result.Kind == "core" {
		return result, nil
	}
	current, contribution, err := m.resolveAccountEdit(ctx, account)
	if err != nil {
		return result, nil
	}
	result.Profile = accountEditProfile(current, contribution)
	bound, release, err := m.bindAccountEditPolicy(ctx, account, current, contribution)
	if err != nil {
		result.Profile.Reason = "account_edit_unavailable"
		return result, nil
	}
	defer release()
	result.Profile.Available = true
	snapshot, err := LoadCindyCatalogSnapshot(bound, account)
	if err != nil {
		result.Catalog = accountEditCatalog(nil)
		return result, nil
	}
	if snapshot.PluginID != current.ID || ValidateAccountEditFresh(bound) != nil {
		result.Profile.Available, result.Profile.Reason = false, "account_edit_unavailable"
		return result, nil
	}
	result.Catalog = accountEditCatalog(snapshot)
	return result, nil
}

func LoadAccountEditContext(ctx context.Context, account *Account) (*AccountEditContext, error) {
	if account == nil || account.ID <= 0 {
		return nil, ErrAccountEditInvalid
	}
	if err := ValidateAccountViewTargets(ctx, []int64{account.ID}); err != nil {
		return nil, err
	}
	provider := processExtensionOperations.Load()
	if provider != nil {
		if resolver, ok := provider.invoker.(interface {
			AccountEditContext(context.Context, *Account) (*AccountEditContext, error)
		}); ok {
			return resolver.AccountEditContext(ctx, account)
		}
	}
	return newAccountEditContext(account, "ctx_pool"), nil
}

type accountEditContextKey struct{}

// A host-private plan is bound to a single actual row. The primary job and view
// identities remain separate; the edit owner never replaces either one.
type BoundAccountEdit struct {
	Execution        PluginExecution
	PrimaryPluginKey string
	AccountID        int64
	StateSHA256      string
	intentSHA256     string
	changed          bool
	fenced           bool
	desired          map[string]accountEditRawValue
	manager          *PluginManager
	installation     *PluginInstallation
	contribution     extensionv1.Contribution
	account          *Account
	lease            *accountEditLease
}

type accountEditLease struct {
	finishOnce  sync.Once
	transaction atomic.Bool
	release     func()
}

func (l *accountEditLease) finish() { l.finishOnce.Do(l.release) }
func (l *accountEditLease) releaseCall() {
	if !l.transaction.Load() {
		l.finish()
	}
}

func AccountEditFromContext(ctx context.Context) (*BoundAccountEdit, bool) {
	if ctx == nil {
		return nil, false
	}
	edit, ok := ctx.Value(accountEditContextKey{}).(*BoundAccountEdit)
	return edit, ok && edit != nil
}

func (m *PluginManager) bindAccountEditPolicy(ctx context.Context, account *Account, current *PluginInstallation, contribution *extensionv1.Contribution) (context.Context, func(), error) {
	runtime, ready := m.accountEditReady(current, contribution, account)
	if !ready {
		return nil, nil, ErrAccountEditUnavailable
	}
	primaryKey := ""
	if execution, bound := PluginExecutionFromContext(ctx); bound {
		primary, err := m.repo.GetByID(ctx, execution.ID)
		if err != nil || primary == nil || primary.RuntimeGeneration != execution.Generation || primary.State != PluginStateEnabled {
			return nil, nil, ErrAccountEditUnavailable
		}
		primaryKey = primary.PluginKey
	}
	bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
	if err != nil {
		return nil, nil, ErrAccountEditUnavailable
	}
	copy := *account
	edit := &BoundAccountEdit{Execution: PluginExecution{current.ID, current.RuntimeGeneration}, PrimaryPluginKey: primaryKey,
		AccountID: account.ID, StateSHA256: AccountEditStateDigest(account), manager: m, installation: current, contribution: *contribution, account: &copy}
	edit.lease = &accountEditLease{release: release}
	return context.WithValue(bound, accountEditContextKey{}, edit), edit.lease.releaseCall, nil
}

func validateAccountEditReference(account *Account, current *PluginInstallation, contribution *extensionv1.Contribution, request *extensionv1.ProviderEditRequestV1) error {
	if request == nil {
		return nil
	}
	if err := validateAccountEditRequest(request); err != nil {
		return err
	}
	if request.ExpectedStateSHA256 != AccountEditStateDigest(account) {
		return ErrAccountEditStateChanged
	}
	if contribution == nil || current == nil || request.ContributionID != contribution.ID || request.ExpectedPackageSHA256 != current.PackageSHA256 ||
		request.ExpectedDefinitionSHA256 != AccountEditDefinitionDigest(contribution) || request.ExpectedRuntimeGeneration != current.RuntimeGeneration {
		return ErrAccountEditUnavailable
	}
	for target, change := range request.Changes {
		if target == "model_mapping" {
			continue
		}
		if target == "compact_model_mapping" {
			if !contribution.AccountEdit.CompactMappingEditable {
				return ErrAccountEditInvalid
			}
			continue
		}
		allowed := false
		for _, control := range contribution.AccountEdit.WireControls {
			if control.Target == target && ((change.Op == "clear" && control.AllowClear) || (change.Op == "set" && change.Value != nil && slices.Contains(control.Values, *change.Value))) {
				allowed = true
			}
		}
		if !allowed {
			return ErrAccountEditInvalid
		}
	}
	return nil
}

func (m *PluginManager) PrepareAccountEdit(ctx context.Context, account *Account, credentials, extra map[string]any, request *extensionv1.ProviderEditRequestV1) (context.Context, func(), error) {
	if err := validateAccountEditRequest(request); err != nil {
		return nil, nil, err
	}
	if !hasCanonicalCindyProviderIdentity(account) || account.ID <= 0 {
		return nil, nil, ErrAccountEditInvalid
	}
	var current *PluginInstallation
	var contribution *extensionv1.Contribution
	var err error
	if request != nil {
		current, contribution, err = m.resolveAccountEdit(ctx, account)
		if err != nil {
			return nil, nil, err
		}
		if err = validateAccountEditReference(account, current, contribution, request); err != nil {
			return nil, nil, err
		}
	}
	var snapshot *CindyCatalogSnapshot
	release := func() {}
	bound := ctx
	if request != nil {
		if _, partition := request.Changes["model_mapping"]; partition {
			if request.ExpectedCatalogNamespace == "" {
				return nil, nil, ErrAccountEditInvalid
			}
			bound, release, err = m.bindAccountEditPolicy(ctx, account, current, contribution)
			if err != nil {
				return nil, nil, err
			}
			snapshot, err = LoadCindyCatalogSnapshot(bound, account)
			if err != nil || snapshot == nil || snapshot.PluginID != current.ID || !snapshot.Config.CatalogEnabled || snapshot.Namespace != request.ExpectedCatalogNamespace {
				release()
				return nil, nil, ErrAccountEditCatalogUnavailable
			}
		}
	}
	desired, changed, err := accountEditDesired(account, credentials, extra, request, snapshot)
	if err != nil {
		release()
		return nil, nil, err
	}
	if changed {
		if current == nil {
			current, contribution, err = m.resolveAccountEdit(ctx, account)
			if err != nil {
				release()
				return nil, nil, err
			}
		}
		if _, already := AccountEditFromContext(bound); !already {
			bound, release, err = m.bindAccountEditPolicy(ctx, account, current, contribution)
			if err != nil {
				return nil, nil, err
			}
		}
		edit, _ := AccountEditFromContext(bound)
		edit.changed, edit.desired, edit.intentSHA256 = true, desired, accountEditIntentDigest(credentials, extra, request)
		return bound, release, nil
	}
	// A catalog-dependent intent had to prove its partition. Release that read
	// lease now; the remaining unchanged write does not need runtime health.
	release()
	edit := &BoundAccountEdit{AccountID: account.ID, StateSHA256: AccountEditStateDigest(account), desired: desired, intentSHA256: accountEditIntentDigest(credentials, extra, request),
		manager: m, installation: current, account: account}
	if contribution != nil {
		edit.contribution = *contribution
	}
	return context.WithValue(ctx, accountEditContextKey{}, edit), func() {}, nil
}

func PrepareAccountEdit(ctx context.Context, account *Account, credentials, extra map[string]any, request *extensionv1.ProviderEditRequestV1) (context.Context, func(), error) {
	if account == nil || account.ID <= 0 {
		return nil, nil, ErrAccountEditInvalid
	}
	if existing, bound := AccountEditFromContext(ctx); bound {
		if existing.AccountID != account.ID || existing.StateSHA256 != AccountEditStateDigest(account) || existing.intentSHA256 != accountEditIntentDigest(credentials, extra, request) {
			return nil, nil, ErrAccountEditStateChanged
		}
		return ctx, func() {}, nil
	}
	if !hasCanonicalCindyProviderIdentity(account) {
		if request != nil {
			return nil, nil, ErrAccountEditInvalid
		}
		return ctx, func() {}, nil
	}
	if request == nil {
		desired, changed, err := accountEditDesired(account, credentials, extra, nil, nil)
		if err != nil {
			return nil, nil, err
		}
		if !changed {
			if !HasAccountEditOwnedInput(credentials, extra) {
				return ctx, func() {}, nil
			}
			return context.WithValue(ctx, accountEditContextKey{}, &BoundAccountEdit{AccountID: account.ID, StateSHA256: AccountEditStateDigest(account), desired: desired, intentSHA256: accountEditIntentDigest(credentials, extra, request)}), func() {}, nil
		}
	}
	provider := processExtensionOperations.Load()
	if provider != nil {
		if binder, ok := provider.invoker.(interface {
			PrepareAccountEdit(context.Context, *Account, map[string]any, map[string]any, *extensionv1.ProviderEditRequestV1) (context.Context, func(), error)
		}); ok {
			return binder.PrepareAccountEdit(ctx, account, credentials, extra, request)
		}
	}
	return nil, nil, ErrAccountEditUnavailable
}

// Called only after ordered installation fences and the account row lock have
// both been acquired. A new delta discovered after re-read must fail closed.
func AccountEditOwnedForUpdate(ctx context.Context, account *Account, credentials, extra map[string]any, request *extensionv1.ProviderEditRequestV1) (map[string]accountEditRawValue, error) {
	if edit, bound := AccountEditFromContext(ctx); bound {
		if edit.AccountID != account.ID || edit.StateSHA256 != AccountEditStateDigest(account) || edit.intentSHA256 != accountEditIntentDigest(credentials, extra, request) {
			return nil, ErrAccountEditStateChanged
		}
		if edit.changed && (!edit.fenced || dbent.TxFromContext(ctx) == nil) {
			return nil, ErrAccountEditUnavailable
		}
		if err := ValidateAccountEditFresh(ctx); err != nil {
			return nil, err
		}
		return edit.desired, nil
	}
	if !hasCanonicalCindyProviderIdentity(account) {
		if request != nil {
			return nil, ErrAccountEditInvalid
		}
		// Ordinary OpenAI retains the approved native mode semantics, but its
		// mapping object (and every other platform) keeps legacy replacement.
		if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
			return nil, nil
		}
		desired, _, err := accountEditDesired(account, nil, extra, nil, nil)
		for _, key := range accountEditCredentialKeys {
			delete(desired, key)
		}
		return desired, err
	}
	desired, changed, err := accountEditDesired(account, credentials, extra, request, nil)
	if err != nil {
		return nil, err
	}
	if hasCanonicalCindyProviderIdentity(account) && (changed || request != nil) {
		return nil, ErrAccountEditUnavailable
	}
	return desired, nil
}

func MarkAccountEditFenced(ctx context.Context) {
	if edit, bound := AccountEditFromContext(ctx); bound {
		edit.fenced = true
	}
}

func ValidateAccountEditFresh(ctx context.Context) error {
	edit, bound := AccountEditFromContext(ctx)
	if !bound || edit.installation == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ErrAccountEditUnavailable
	}
	current, err := edit.manager.repo.GetByID(ctx, edit.installation.ID)
	if err != nil || current == nil || !samePluginRuntime(current, edit.installation) || current.Revision != edit.installation.Revision || current.ConfigEncrypted != edit.installation.ConfigEncrypted {
		return ErrAccountEditUnavailable
	}
	contribution := accountEditContribution(current, edit.contribution.ID)
	if AccountEditDefinitionDigest(contribution) != AccountEditDefinitionDigest(&edit.contribution) {
		return ErrAccountEditUnavailable
	}
	if edit.changed || edit.Execution.ID > 0 {
		if _, ready := edit.manager.accountEditReady(current, contribution, edit.account); !ready {
			return ErrAccountEditUnavailable
		}
	}
	return nil
}

// Database fence validation uses bindings read while holding the same owner row
// lock as config/package changes. Unlike Create it uses the real stable bucket.
func ValidateAccountEditFenceData(fence PluginExecutionFence, manifestJSON []byte, encryptedConfig string, bindings []PluginBinding) error {
	if accountCreateConfigDigest(encryptedConfig) != fence.ConfigSHA256 || fence.EditAccountID <= 0 {
		return ErrAccountEditUnavailable
	}
	var manifest PluginManifest
	if json.Unmarshal(manifestJSON, &manifest) != nil || manifest.ID != fence.PluginKey || validateAccountEditContributions(manifest) != nil {
		return ErrAccountEditUnavailable
	}
	installation := &PluginInstallation{ID: fence.ID, PluginKey: fence.PluginKey, Manifest: manifest, Bindings: bindings}
	contribution := accountEditContribution(installation, fence.EditContributionID)
	if contribution == nil || AccountEditDefinitionDigest(contribution) != fence.EditDefinitionSHA256 || !contributionAccountAllowed(installation, contribution, extensionv1.Account{ID: fence.EditAccountID, Platform: PlatformCindy, Type: AccountTypeAPIKey}) {
		return ErrAccountEditUnavailable
	}
	return nil
}

func retainAccountEditUntilTransactionEnds(ctx context.Context, tx *dbent.Tx, release func()) {
	var once sync.Once
	finish := func() { once.Do(release) }
	tx.OnCommit(func(next dbent.Committer) dbent.Committer {
		return dbent.CommitFunc(func(commitCtx context.Context, transaction *dbent.Tx) error {
			if err := ValidateAccountEditFresh(ctx); err != nil {
				return err
			}
			err := next.Commit(commitCtx, transaction)
			if err == nil {
				finish()
			}
			return err
		})
	})
	tx.OnRollback(func(next dbent.Rollbacker) dbent.Rollbacker {
		return dbent.RollbackFunc(func(rollbackCtx context.Context, transaction *dbent.Tx) error {
			err := next.Rollback(rollbackCtx, transaction)
			finish()
			return err
		})
	})
}

func retainBoundAccountEditLease(ctx context.Context, tx *dbent.Tx) {
	edit, bound := AccountEditFromContext(ctx)
	if !bound || edit.lease == nil || !edit.changed || !edit.lease.transaction.CompareAndSwap(false, true) {
		return
	}
	retainAccountEditUntilTransactionEnds(ctx, tx, edit.lease.finish)
}

// Used by a legacy bulk merge: codec keys are native full replacements here.
// No public bulk intent or clear capability is introduced.
func mergeAccountEditBulkExtra(current, patch map[string]any) map[string]any {
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, patch)
	return merged
}
