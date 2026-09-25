package service

import "maps"

// accountEditExtraKeys are the OpenAI API-key protocol-mode extras. A
// whole-object account edit replaces Extra, but an omitted key keeps its stored
// presence and value instead of silently falling back to the default mode.
var accountEditExtraKeys = []string{
	"openai_responses_mode", "openai_compact_mode",
	"openai_apikey_responses_websockets_v2_mode", "openai_apikey_responses_websockets_v2_enabled",
	"responses_websockets_v2_enabled", "openai_ws_enabled",
	OpenAIPromptCacheKeyModeExtraKey,
}

type accountEditRawValue struct {
	Present bool `json:"present"`
	Value   any  `json:"value"`
}

// openAIAPIKeyOwnedExtraForUpdate resolves the protocol-mode keys an update
// writes: supplied values win, omitted keys retain their current raw state.
func openAIAPIKeyOwnedExtraForUpdate(account *Account, extra map[string]any) map[string]accountEditRawValue {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return nil
	}
	desired := make(map[string]accountEditRawValue, len(accountEditExtraKeys))
	for _, key := range accountEditExtraKeys {
		if value, present := extra[key]; present {
			desired[key] = accountEditRawValue{true, value}
		} else if value, present := account.Extra[key]; present {
			desired[key] = accountEditRawValue{true, value}
		} else {
			desired[key] = accountEditRawValue{}
		}
	}
	return desired
}

func applyAccountEditOwned(account *Account, values map[string]accountEditRawValue) {
	if len(values) == 0 {
		return
	}
	account.Extra = maps.Clone(account.Extra)
	if account.Extra == nil {
		account.Extra = map[string]any{}
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
}
