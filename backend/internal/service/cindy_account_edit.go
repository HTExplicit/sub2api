package service

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
)

var (
	ErrAccountEditInvalid            = infraerrors.BadRequest("ACCOUNT_EDIT_INVALID_REQUEST", "invalid provider edit input")
	ErrAccountEditUnavailable        = infraerrors.Conflict("ACCOUNT_EDIT_UNAVAILABLE", "provider editing is unavailable or has changed")
	ErrAccountEditStateChanged       = infraerrors.Conflict("ACCOUNT_EDIT_STATE_CHANGED", "account edit state has changed")
	ErrAccountEditCatalogUnavailable = infraerrors.Conflict("ACCOUNT_EDIT_CATALOG_UNAVAILABLE", "provider edit catalog is unavailable or has changed")
)

type AccountEditProfileReference struct {
	Native            bool   `json:"native,omitempty"`
	PolicySHA256      string `json:"policy_sha256,omitempty"`
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

func AccountEditFromContext(ctx context.Context) (*BoundAccountEdit, bool) {
	if ctx == nil {
		return nil, false
	}
	edit, ok := ctx.Value(accountEditContextKey{}).(*BoundAccountEdit)
	return edit, ok && edit != nil
}

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

func mergeAccountEditBulkExtra(current, patch map[string]any) map[string]any {
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, patch)
	return merged
}

// A native edit holds the current policy until its account transaction ends.
// No installation, package or plugin execution identity authorizes this edit.
type accountEditContextKey struct{}
type BoundAccountEdit struct {
	AccountID        int64
	StateSHA256      string
	intentSHA256     string
	changed          bool
	fenced           bool
	desired          map[string]accountEditRawValue
	runtime          *cindyProviderRuntime
	catalogNamespace string
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

func nativeAccountEditProfile(runtimes ...*cindyProviderRuntime) *AccountEditProfileReference {
	runtime := cindyProvider.Load()
	if len(runtimes) > 0 && runtimes[0] != nil {
		runtime = runtimes[0]
	}
	return &AccountEditProfileReference{Native: true, Available: true, PolicySHA256: runtime.policySHA256}
}

func LoadAccountEditContext(ctx context.Context, account *Account, wsDefaults ...string) (*AccountEditContext, error) {
	if account == nil || account.ID <= 0 {
		return nil, ErrAccountEditInvalid
	}
	wsDefault := "ctx_pool"
	if len(wsDefaults) > 0 {
		wsDefault = wsDefaults[0]
	}
	result := newAccountEditContext(account, wsDefault)
	if result.Kind == "core" {
		return result, nil
	}
	result.Profile = nativeAccountEditProfile()
	snapshot, err := LoadCindyCatalogSnapshot(ctx, account)
	if err != nil {
		result.Catalog = accountEditCatalog(nil)
	} else {
		result.Catalog = accountEditCatalog(snapshot)
	}
	return result, nil
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
	if err := validateAccountEditRequest(request); err != nil {
		return nil, nil, err
	}
	if request != nil && request.ExpectedStateSHA256 != AccountEditStateDigest(account) {
		return nil, nil, ErrAccountEditStateChanged
	}
	cindyProviderPolicyMu.RLock()
	lease := &accountEditLease{release: cindyProviderPolicyMu.RUnlock}
	runtime := cindyProvider.Load()
	var snapshot *CindyCatalogSnapshot
	if request != nil {
		if _, partition := request.Changes["model_mapping"]; partition {
			var err error
			snapshot, err = LoadCindyCatalogSnapshot(ctx, account)
			if err != nil || snapshot == nil || !snapshot.Config.CatalogEnabled || request.ExpectedCatalogNamespace == "" || snapshot.Namespace != request.ExpectedCatalogNamespace {
				lease.finish()
				return nil, nil, ErrAccountEditCatalogUnavailable
			}
		}
	}
	desired, changed, err := accountEditDesired(account, credentials, extra, request, snapshot)
	if err != nil {
		lease.finish()
		return nil, nil, err
	}
	edit := &BoundAccountEdit{AccountID: account.ID, StateSHA256: AccountEditStateDigest(account),
		desired: desired, changed: changed, intentSHA256: accountEditIntentDigest(credentials, extra, request), account: account}
	if changed {
		edit.runtime, edit.lease = runtime, lease
		if snapshot != nil {
			edit.catalogNamespace = snapshot.Namespace
		}
		return context.WithValue(ctx, accountEditContextKey{}, edit), lease.releaseCall, nil
	}
	lease.finish()
	return context.WithValue(ctx, accountEditContextKey{}, edit), func() {}, nil
}

func ValidateAccountEditFresh(ctx context.Context) error {
	edit, bound := AccountEditFromContext(ctx)
	if !bound || !edit.changed {
		return nil
	}
	if ctx.Err() != nil || edit.runtime != cindyProvider.Load() {
		return ErrAccountEditUnavailable
	}
	if edit.catalogNamespace != "" {
		snapshot, err := LoadCindyCatalogSnapshot(ctx, edit.account)
		if err != nil || snapshot.Namespace != edit.catalogNamespace {
			return ErrAccountEditCatalogUnavailable
		}
	}
	return nil
}
