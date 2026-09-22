package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var (
	ErrAccountCreateInvalid        = infraerrors.BadRequest("ACCOUNT_CREATE_INVALID_REQUEST", "invalid provider create input")
	ErrAccountCreateUnavailable    = infraerrors.Conflict("ACCOUNT_CREATE_UNAVAILABLE", "provider creation is unavailable or has changed")
	ErrAccountCreateGroupsRequired = infraerrors.BadRequest("ACCOUNT_CREATE_GROUP_REQUIRED", "provider account requires an effective group")
)

var accountCreateDefaultTargets = []string{"concurrency", "priority", "rate_multiplier", "load_factor", "responses_mode"}

func validateAccountCreateContributions(manifest PluginManifest) error {
	seen := map[string]bool{}
	for _, contribution := range manifest.Contributions {
		definition := contribution.AccountCreate
		if contribution.Slot != extensionv1.AccountCreateSlot {
			if definition != nil {
				return ErrAccountCreateInvalid
			}
			continue
		}
		if definition == nil || manifest.ID != CindyAccountViewPluginKey || !accountViewIDPattern.MatchString(contribution.ID) ||
			contribution.Capability != extensionv1.CapabilityProvider || contribution.Permission != "admin" ||
			contribution.Entrypoint != "" || contribution.Action != "" || contribution.AccountFilter != nil ||
			contribution.AccountView != nil || contribution.ResourceAction != nil || contribution.AllAccounts || contribution.RetainedControls ||
			len(contribution.DisplayFields) != 0 || len(contribution.ValueBindings) != 0 || len(contribution.Events) != 0 ||
			!accountViewLabelValid(contribution.Label) {
			return ErrAccountCreateInvalid
		}
		adminDeclared := false
		for _, capability := range manifest.Capabilities {
			adminDeclared = adminDeclared || capability.ID == extensionv1.CapabilityAdmin
		}
		if !adminDeclared || definition.Version != 1 || definition.Platform != PlatformCindy || definition.AccountType != AccountTypeAPIKey || definition.CredentialProfile != ProviderProfileCindyLaxaV1 || seen[definition.CredentialProfile] {
			return ErrAccountCreateInvalid
		}
		seen[definition.CredentialProfile] = true
		ui := definition.CredentialUI
		endpoint, err := NormalizeCredentialIdentityBaseURL(definition.CredentialProfile, ui.BaseURL)
		if err != nil || endpoint != "https://api.laxarouter.ai" || ui.BaseURL != endpoint || !ui.BaseURLReadonly || len(ui.APIKeyPlaceholder) < 1 || len(ui.APIKeyPlaceholder) > 128 || !accountViewLabelValid(ui.Hint) {
			return ErrAccountCreateInvalid
		}
		for _, label := range []map[string]string{ui.BaseURLHint, ui.AccountTypeHint, ui.CatalogLabel, ui.CatalogHint} {
			if label != nil && !accountViewLabelValid(label) {
				return ErrAccountCreateInvalid
			}
		}
		defaults := definition.Defaults
		if defaults.Concurrency < 1 || defaults.Priority < 0 || defaults.RateMultiplier < 0 || math.IsNaN(defaults.RateMultiplier) || math.IsInf(defaults.RateMultiplier, 0) ||
			(defaults.LoadFactor != nil && (*defaults.LoadFactor < 1 || *defaults.LoadFactor > 10000)) ||
			!slices.Contains([]string{"auto", "force_responses", "force_chat_completions"}, defaults.ResponsesMode) ||
			definition.MinimumEffectiveGroups != 1 || definition.UpstreamBillingProbe != "unsupported" || definition.ModelEditing != "provider_managed" || definition.CatalogSource != "group_model_candidates" {
			return ErrAccountCreateInvalid
		}
		if len(contribution.Fields) > 8 || len(definition.FieldBindings) != len(contribution.Fields) {
			return ErrAccountCreateInvalid
		}
		keys, targets := map[string]bool{}, map[string]bool{}
		for _, field := range contribution.Fields {
			target, exists := definition.FieldBindings[field.Key]
			if !exists || !accountViewIDPattern.MatchString(field.Key) || keys[field.Key] || targets[target] || target != "provider_device_identity_input" ||
				field.Kind != "text" || field.MaxLength < 1 || field.MaxLength > 64 || field.Rows != 0 || field.OptionsSource != "" || field.DefaultSource != "" || len(field.DefaultLabel) != 0 ||
				!accountViewLabelValid(field.Label) || (field.Hint != nil && !accountViewLabelValid(field.Hint)) {
				return ErrAccountCreateInvalid
			}
			keys[field.Key], targets[target] = true, true
		}
	}
	return nil
}

func AccountCreateDefinitionDigest(contribution *extensionv1.Contribution) string {
	if contribution == nil || contribution.AccountCreate == nil {
		return ""
	}
	raw, err := json.Marshal(contribution)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateAccountCreateRegistry(installations []*PluginInstallation) error {
	owners := map[string]int64{}
	for _, installation := range installations {
		if installation == nil {
			continue
		}
		for _, contribution := range installation.Manifest.Contributions {
			if contribution.Slot != extensionv1.AccountCreateSlot || contribution.AccountCreate == nil {
				continue
			}
			profile := contribution.AccountCreate.CredentialProfile
			if owner, exists := owners[profile]; exists && owner != installation.ID {
				return ErrAccountCreateUnavailable
			}
			owners[profile] = installation.ID
		}
	}
	return nil
}

func accountCreateScopeAllowed(installation *PluginInstallation, contribution *extensionv1.Contribution) bool {
	if contribution == nil || contribution.AccountCreate == nil {
		return false
	}
	definition := contribution.AccountCreate
	for _, binding := range contributionEffectiveBindings(installation, contribution) {
		if binding.RolloutPercent == 100 && pluginScopeMatches(binding.Platform, definition.Platform) && pluginScopeMatches(binding.AccountType, definition.AccountType) {
			return true
		}
	}
	return false
}

type accountCreateContextKey struct{}

// BoundAccountCreate is host-private and independent of the existing job owner.
// No credentials, native account object or generated device value enter the SDK.
type BoundAccountCreate struct {
	Execution        PluginExecution
	PrimaryPluginKey string
	manager          *PluginManager
	installation     *PluginInstallation
	contribution     extensionv1.Contribution
	request          *extensionv1.ProviderCreateRequestV1
}

func AccountCreateFromContext(ctx context.Context) (*BoundAccountCreate, bool) {
	if ctx == nil {
		return nil, false
	}
	create, ok := ctx.Value(accountCreateContextKey{}).(*BoundAccountCreate)
	return create, ok && create != nil
}

func accountCreateContribution(installation *PluginInstallation, id string) *extensionv1.Contribution {
	var found *extensionv1.Contribution
	if installation == nil {
		return nil
	}
	for _, contribution := range installation.Manifest.Contributions {
		if contribution.ID == id && contribution.Slot == extensionv1.AccountCreateSlot && contribution.AccountCreate != nil {
			if found != nil {
				return nil
			}
			copy := contribution
			found = &copy
		}
	}
	return found
}

func (m *PluginManager) accountCreateReady(current *PluginInstallation, contribution *extensionv1.Contribution) (*pluginRuntime, bool) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" || current == nil || current.State != PluginStateEnabled || current.RuntimeGeneration <= 0 ||
		current.PluginKey != CindyAccountViewPluginKey || current.Manifest.ID != current.PluginKey || !samePluginRuntime(current, registry.installations[current.ID]) ||
		contribution == nil || validateAccountCreateContributions(current.Manifest) != nil || !accountCreateScopeAllowed(current, contribution) {
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

func (m *PluginManager) BindAccountCreate(ctx context.Context, platform, accountType, profile string, request *extensionv1.ProviderCreateRequestV1) (context.Context, func(), error) {
	if platform != PlatformCindy || accountType != AccountTypeAPIKey || profile != ProviderProfileCindyLaxaV1 || m == nil || m.repo == nil {
		return nil, nil, ErrAccountCreateUnavailable
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return nil, nil, ErrAccountCreateUnavailable
	}
	var ownerID int64
	var contributionID string
	for id, installation := range registry.installations {
		for _, contribution := range installation.Manifest.Contributions {
			if contribution.Slot != extensionv1.AccountCreateSlot || contribution.AccountCreate == nil || contribution.AccountCreate.CredentialProfile != profile {
				continue
			}
			if ownerID != 0 || installation.PluginKey != CindyAccountViewPluginKey {
				return nil, nil, ErrAccountCreateUnavailable
			}
			ownerID, contributionID = id, contribution.ID
		}
	}
	if ownerID == 0 {
		return nil, nil, ErrAccountCreateUnavailable
	}
	current, err := m.repo.GetByID(ctx, ownerID)
	if err != nil {
		return nil, nil, ErrAccountCreateUnavailable
	}
	contribution := accountCreateContribution(current, contributionID)
	runtime, ready := m.accountCreateReady(current, contribution)
	if !ready {
		return nil, nil, ErrAccountCreateUnavailable
	}
	if request != nil {
		if !accountViewIDPattern.MatchString(request.ContributionID) || !accountViewDigestPattern.MatchString(request.ExpectedPackageSHA256) || !accountViewDigestPattern.MatchString(request.ExpectedDefinitionSHA256) || request.ExpectedRuntimeGeneration <= 0 || len(request.Values) > 8 || len(request.InheritDefaults) > 5 {
			return nil, nil, ErrAccountCreateInvalid
		}
		if request.ContributionID != contribution.ID || request.ExpectedPackageSHA256 != current.PackageSHA256 || request.ExpectedDefinitionSHA256 != AccountCreateDefinitionDigest(contribution) || request.ExpectedRuntimeGeneration != current.RuntimeGeneration {
			return nil, nil, ErrAccountCreateUnavailable
		}
		seen := map[string]bool{}
		for _, target := range request.InheritDefaults {
			if !slices.Contains(accountCreateDefaultTargets, target) || seen[target] {
				return nil, nil, ErrAccountCreateInvalid
			}
			seen[target] = true
		}
		for key, value := range request.Values {
			valid := false
			for _, field := range contribution.Fields {
				valid = valid || (key == field.Key && len([]rune(value)) <= field.MaxLength)
			}
			if !valid {
				return nil, nil, ErrAccountCreateInvalid
			}
		}
		copy := *request
		copy.Values, copy.InheritDefaults = maps.Clone(request.Values), slices.Clone(request.InheritDefaults)
		request = &copy
	}
	primaryKey := ""
	if execution, bound := PluginExecutionFromContext(ctx); bound {
		primary, err := m.repo.GetByID(ctx, execution.ID)
		if err != nil || primary == nil || primary.RuntimeGeneration != execution.Generation || primary.State != PluginStateEnabled {
			return nil, nil, ErrAccountCreateUnavailable
		}
		primaryKey = primary.PluginKey
	}
	bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
	if err != nil {
		return nil, nil, ErrAccountCreateUnavailable
	}
	create := &BoundAccountCreate{Execution: PluginExecution{current.ID, current.RuntimeGeneration}, PrimaryPluginKey: primaryKey, manager: m, installation: current, contribution: *contribution, request: request}
	return context.WithValue(bound, accountCreateContextKey{}, create), release, nil
}

func bindProcessAccountCreate(ctx context.Context, platform, accountType, profile string, request *extensionv1.ProviderCreateRequestV1) (context.Context, func(), error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrAccountCreateUnavailable
	}
	binder, ok := provider.invoker.(interface {
		BindAccountCreate(context.Context, string, string, string, *extensionv1.ProviderCreateRequestV1) (context.Context, func(), error)
	})
	if !ok {
		return nil, nil, ErrAccountCreateUnavailable
	}
	return binder.BindAccountCreate(ctx, platform, accountType, profile, request)
}

func ValidateAccountCreateFresh(ctx context.Context) error {
	create, bound := AccountCreateFromContext(ctx)
	if !bound {
		return nil
	}
	if ctx.Err() != nil {
		return ErrAccountCreateUnavailable
	}
	current, err := create.manager.repo.GetByID(ctx, create.Execution.ID)
	if err != nil || current == nil || current.Revision != create.installation.Revision || current.ConfigEncrypted != create.installation.ConfigEncrypted || !samePluginRuntime(current, create.installation) {
		return ErrAccountCreateUnavailable
	}
	contribution := accountCreateContribution(current, create.contribution.ID)
	if _, ready := create.manager.accountCreateReady(current, contribution); !ready || AccountCreateDefinitionDigest(contribution) != AccountCreateDefinitionDigest(&create.contribution) {
		return ErrAccountCreateUnavailable
	}
	return nil
}

func accountCreateConfigDigest(encrypted string) string {
	digest := sha256.Sum256([]byte(encrypted))
	return hex.EncodeToString(digest[:])
}

// ValidateAccountCreateFenceData validates data read under the shared installation
// row lock in the actual account transaction. No secret is returned or logged.
func ValidateAccountCreateFenceData(fence PluginExecutionFence, manifestJSON []byte, encryptedConfig string, fullProviderAdmin bool) error {
	if !fullProviderAdmin || accountCreateConfigDigest(encryptedConfig) != fence.ConfigSHA256 {
		return ErrAccountCreateUnavailable
	}
	var manifest PluginManifest
	if json.Unmarshal(manifestJSON, &manifest) != nil || manifest.ID != fence.PluginKey || validateAccountCreateContributions(manifest) != nil {
		return ErrAccountCreateUnavailable
	}
	contribution := accountCreateContribution(&PluginInstallation{Manifest: manifest}, fence.CreateContributionID)
	if AccountCreateDefinitionDigest(contribution) != fence.CreateDefinitionSHA256 {
		return ErrAccountCreateUnavailable
	}
	return nil
}

// An outer batch/import transaction owns the create lease until actual commit or
// rollback. Returning from the nested service call must not release it early.
func retainAccountCreateUntilTransactionEnds(ctx context.Context, tx *dbent.Tx, release func()) {
	var once sync.Once
	finish := func() { once.Do(release) }
	tx.OnCommit(func(next dbent.Committer) dbent.Committer {
		return dbent.CommitFunc(func(commitCtx context.Context, transaction *dbent.Tx) error {
			if err := ValidateAccountCreateFresh(ctx); err != nil {
				return err // rollback owns release when commit is rejected
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

func applyAccountCreateProfile(ctx context.Context, input *CreateAccountInput) error {
	create, bound := AccountCreateFromContext(ctx)
	if !bound || ValidateAccountCreateFresh(ctx) != nil {
		return ErrAccountCreateUnavailable
	}
	definition := create.contribution.AccountCreate
	if input.ProbeEnabled != nil && *input.ProbeEnabled {
		return ErrAccountCreateInvalid
	}
	if len(input.ModelContextOverrides) != 0 {
		return ErrAccountCreateInvalid
	}
	input.Extra = maps.Clone(input.Extra)
	if input.Extra == nil {
		input.Extra = map[string]any{}
	}
	request := create.request
	if request != nil {
		for key, value := range request.Values {
			if create.contribution.AccountCreate.FieldBindings[key] != "provider_device_identity_input" {
				return ErrAccountCreateInvalid
			}
			value = strings.TrimSpace(value)
			if existing, present := input.Extra[CindyDeviceIDExtraKey]; present {
				text, valid := existing.(string)
				if !valid || strings.TrimSpace(text) != value {
					return ErrAccountCreateInvalid
				}
			}
			if value != "" {
				input.Extra[CindyDeviceIDExtraKey] = value
			} else {
				delete(input.Extra, CindyDeviceIDExtraKey)
			}
		}
		for _, target := range request.InheritDefaults {
			if accountCreateValueExplicit(input, target) {
				continue
			}
			switch target {
			case "concurrency":
				input.Concurrency = definition.Defaults.Concurrency
			case "priority":
				input.Priority = definition.Defaults.Priority
			case "rate_multiplier":
				value := definition.Defaults.RateMultiplier
				input.RateMultiplier = &value
			case "load_factor":
				input.LoadFactor = nil
				if definition.Defaults.LoadFactor != nil {
					value := *definition.Defaults.LoadFactor
					input.LoadFactor = &value
				}
			}
		}
	}
	if mode, present := input.Extra[CindyResponsesModeExtraKey]; present {
		text, valid := mode.(string)
		if !valid || !slices.Contains([]string{"auto", "force_responses", "force_chat_completions"}, text) {
			return ErrAccountCreateInvalid
		}
	} else {
		input.Extra[CindyResponsesModeExtraKey] = definition.Defaults.ResponsesMode
	}
	// Creation derives provenance from the actual input, not a client assertion.
	// Keep the legacy input validator even though valid claims are not trusted.
	if source, present := input.Extra[CindyDeviceIDSourceExtraKey]; present {
		if _, valid := normalizeCindyDeviceIDSource(source); !valid {
			return infraerrors.BadRequest("CINDY_DEVICE_ID_SOURCE_INVALID", "cindy_device_id_source is invalid")
		}
	}
	delete(input.Extra, CindyDeviceIDSourceExtraKey)
	return nil
}

func accountCreateValueExplicit(input *CreateAccountInput, target string) bool {
	if input.ExplicitCreateFields != nil {
		return input.ExplicitCreateFields[target]
	}
	// Direct Go callers can supply the presence map for explicit zero/null.
	// Existing untagged callers never enter numeric inheritance at all.
	switch target {
	case "concurrency":
		return input.Concurrency != 0
	case "priority":
		return input.Priority != 0
	case "rate_multiplier":
		return input.RateMultiplier != nil
	case "load_factor":
		return input.LoadFactor != nil
	}
	return false
}
