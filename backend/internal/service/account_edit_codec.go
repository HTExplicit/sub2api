package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// The deletion vocabulary is private and finite. Neither an HTTP client nor a
// plugin can supply arbitrary JSON paths or keys to remove.
var accountEditDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var accountEditExtraKeys = []string{
	"openai_responses_mode", "openai_compact_mode",
	"openai_apikey_responses_websockets_v2_mode", "openai_apikey_responses_websockets_v2_enabled",
	"responses_websockets_v2_enabled", "openai_ws_enabled",
}
var accountEditCredentialKeys = []string{"model_mapping", "compact_model_mapping"}
var accountEditModeValues = map[string][]string{
	"responses_mode":           {"auto", "force_responses", "force_chat_completions"},
	"compact_mode":             {"auto", "force_on", "force_off"},
	"responses_websocket_mode": {"off", "ctx_pool", "passthrough", "http_bridge"},
}

func accountEditTargetKeys(target string) []string {
	switch target {
	case "responses_mode":
		return accountEditExtraKeys[:1]
	case "compact_mode":
		return accountEditExtraKeys[1:2]
	case "responses_websocket_mode":
		return accountEditExtraKeys[2:]
	}
	return nil
}

type accountEditRawValue struct {
	Present bool `json:"present"`
	Value   any  `json:"value"`
}

func accountEditRawState(account *Account) map[string]accountEditRawValue {
	state := make(map[string]accountEditRawValue, 8)
	for _, key := range accountEditExtraKeys {
		value, present := account.Extra[key]
		state[key] = accountEditRawValue{present, value}
	}
	for _, key := range accountEditCredentialKeys {
		value, present := account.Credentials[key]
		state[key] = accountEditRawValue{present, value}
	}
	return state
}

// Only owned raw keys/presence, canonical identity and credential generation
// participate. Usage, observations, whole Extra and raw secrets do not.
func AccountEditStateDigest(account *Account) string {
	if account == nil || account.ID <= 0 {
		return ""
	}
	raw, err := json.Marshal(struct {
		ID                   int64                          `json:"id"`
		Platform             string                         `json:"platform"`
		Wire                 string                         `json:"wire"`
		Profile              string                         `json:"profile"`
		Type                 string                         `json:"type"`
		Endpoint             string                         `json:"endpoint"`
		CredentialGeneration int64                          `json:"credential_generation"`
		Fields               map[string]accountEditRawValue `json:"fields"`
	}{account.ID, account.Platform, account.EffectiveWirePlatform(), account.EffectiveProviderProfile(), account.Type,
		account.GetCredential("base_url"), account.CindyCredentialGeneration, accountEditRawState(account)})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func accountEditJSONEqual(a, b any) bool {
	left, lerr := json.Marshal(a)
	right, rerr := json.Marshal(b)
	return lerr == nil && rerr == nil && bytes.Equal(left, right)
}

func accountEditIntentDigest(credentials, extra map[string]any, request *extensionv1.ProviderEditRequestV1) string {
	raw, err := json.Marshal(struct {
		Fields  map[string]accountEditRawValue     `json:"fields"`
		Request *extensionv1.ProviderEditRequestV1 `json:"request"`
	}{accountEditRawState(&Account{Credentials: credentials, Extra: extra}), request})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func HasAccountEditOwnedInput(credentials, extra map[string]any) bool {
	for _, key := range accountEditExtraKeys {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	for _, key := range accountEditCredentialKeys {
		if _, ok := credentials[key]; ok {
			return true
		}
	}
	return false
}

func validateAccountEditRequest(request *extensionv1.ProviderEditRequestV1) error {
	if request == nil {
		return nil
	}
	if !accountEditDigestPattern.MatchString(request.ExpectedStateSHA256) || request.Changes == nil || len(request.Changes) > 5 || len(request.ExpectedCatalogNamespace) > 512 {
		return ErrAccountEditInvalid
	}

	for target, change := range request.Changes {
		if change.Op != "set" && change.Op != "clear" {
			return ErrAccountEditInvalid
		}
		values, mode := accountEditModeValues[target]
		if !mode && !slices.Contains(accountEditCredentialKeys, target) {
			return ErrAccountEditInvalid
		}
		if mode && change.Op == "set" {
			if change.Value == nil || !slices.Contains(values, *change.Value) {
				return ErrAccountEditInvalid
			}
		} else if change.Value != nil {
			return ErrAccountEditInvalid
		}
	}
	return nil
}

func accountEditStringMap(raw any) (map[string]any, error) {
	out := map[string]any{}
	switch values := raw.(type) {
	case map[string]any:
		for key, value := range values {
			if _, ok := value.(string); !ok {
				return nil, ErrAccountEditInvalid
			}
			out[key] = value
		}
	case map[string]string:
		for key, value := range values {
			out[key] = value
		}
	default:
		return nil, ErrAccountEditInvalid
	}
	return out, nil
}

// Partition only pairs that are already stored; never materialize the provider
// inventory in account credentials. Input wins, matching existing UI/runtime
// precedence (including useful custom pairs shadowed by provider routing).
func accountEditStoredManagedPairs(raw any, snapshot *CindyCatalogSnapshot) map[string]any {
	managed := map[string]any{}
	var stored map[string]any
	switch values := raw.(type) {
	case map[string]any:
		stored = values
	case map[string]string:
		stored = map[string]any{}
		for k, v := range values {
			stored[k] = v
		}
	}
	if snapshot == nil {
		return managed
	}
	models := map[string]extensionv1.CindyCatalogModel{}
	public := map[string]extensionv1.CindyCatalogModel{}
	for _, model := range snapshot.CatalogModels {
		if model.Managed {
			models[model.ID] = model
		}
		if model.PublicModel {
			public[model.ID] = model
		}
	}
	for alias, target := range snapshot.CompatibilityAliases {
		if model, ok := public[target]; ok {
			model.ID, model.AliasTarget = alias, target
			models[alias] = model
		}
	}
	for from, to := range stored {
		text, stringValue := to.(string)
		if !stringValue {
			continue
		}
		model, ok := models[strings.TrimSpace(from)]
		value := strings.TrimSpace(text)
		if ok && (value == model.ID || value == model.AliasTarget || value == model.LiveUpstreamID) {
			managed[from] = to
		}
	}
	return managed
}

// Resolve the exact owned subset from current persisted values. Omission is
// lossless retention for both native legacy and typed requests. Legacy supplied
// maps remain full replacements and explicit null remains a present JSON null.
func accountEditDesired(account *Account, credentials, extra map[string]any, request *extensionv1.ProviderEditRequestV1, snapshot *CindyCatalogSnapshot) (map[string]accountEditRawValue, bool, error) {
	if err := validateAccountEditRequest(request); err != nil {
		return nil, false, err
	}
	before := accountEditRawState(account)
	after := maps.Clone(before)
	if request == nil {
		for _, key := range accountEditExtraKeys {
			if value, present := extra[key]; present {
				after[key] = accountEditRawValue{true, value}
			}
		}
		for _, key := range accountEditCredentialKeys {
			if value, present := credentials[key]; present {
				after[key] = accountEditRawValue{true, value}
			}
		}
	} else {
		for _, key := range accountEditExtraKeys {
			// A repeated identical raw value is harmless, but typed intent is the
			// sole writer. Contradictory raw input is never silently discarded.
			changedTarget := false
			for target := range request.Changes {
				changedTarget = changedTarget || slices.Contains(accountEditTargetKeys(target), key)
			}
			if value, present := extra[key]; present && !changedTarget && !accountEditJSONEqual(accountEditRawValue{true, value}, before[key]) {
				return nil, false, ErrAccountEditInvalid
			}
		}
		for _, key := range accountEditCredentialKeys {
			change, changed := request.Changes[key]
			value, present := credentials[key]
			if present && (!changed || change.Op != "set") && !accountEditJSONEqual(accountEditRawValue{true, value}, before[key]) {
				return nil, false, ErrAccountEditInvalid
			}
			if changed && change.Op == "set" && !present {
				return nil, false, ErrAccountEditInvalid
			}
			if changed && change.Op == "clear" && present {
				return nil, false, ErrAccountEditInvalid
			}
		}
		for target, change := range request.Changes {
			if keys := accountEditTargetKeys(target); len(keys) > 0 {
				if change.Op == "clear" {
					for _, key := range keys {
						after[key] = accountEditRawValue{}
					}
				} else {
					after[keys[0]] = accountEditRawValue{true, *change.Value}
				}
				for _, key := range keys {
					if value, present := extra[key]; present && !accountEditJSONEqual(accountEditRawValue{true, value}, after[key]) {
						return nil, false, ErrAccountEditInvalid
					}
				}
				continue
			}
			var values map[string]any
			if change.Op == "set" {
				var err error
				values, err = accountEditStringMap(credentials[target])
				if err != nil {
					return nil, false, err
				}
			}
			if target == "model_mapping" {
				if snapshot == nil || !snapshot.Config.CatalogEnabled || request.ExpectedCatalogNamespace != snapshot.Namespace {
					return nil, false, ErrAccountEditCatalogUnavailable
				}
				merged := accountEditStoredManagedPairs(account.Credentials[target], snapshot)
				maps.Copy(merged, values)
				values = merged
			}
			if change.Op == "clear" && len(values) == 0 {
				after[target] = accountEditRawValue{}
			} else {
				after[target] = accountEditRawValue{true, values}
			}
		}
	}
	return after, !accountEditJSONEqual(before, after), nil
}

func applyAccountEditOwned(account *Account, values map[string]accountEditRawValue) {
	if len(values) == 0 {
		return
	}
	account.Extra = maps.Clone(account.Extra)
	account.Credentials = maps.Clone(account.Credentials)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	if account.Credentials == nil {
		account.Credentials = map[string]any{}
	}
	for _, key := range accountEditExtraKeys {
		value, owned := values[key]
		if !owned {
			continue
		}
		if value.Present {
			account.Extra[key] = value.Value
		} else {
			delete(account.Extra, key)
		}
	}
	for _, key := range accountEditCredentialKeys {
		value, owned := values[key]
		if !owned {
			continue
		}
		if value.Present {
			account.Credentials[key] = value.Value
		} else {
			delete(account.Credentials, key)
		}
	}
}
