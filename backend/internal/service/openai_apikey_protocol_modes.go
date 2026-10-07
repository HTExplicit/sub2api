package service

import "maps"

// openAIAPIKeyProtocolModeExtraKeys are the OpenAI API-key protocol-mode
// extras. A whole-object account edit replaces Extra, but an omitted key keeps
// its stored presence and value instead of silently falling back to the
// default mode.
var openAIAPIKeyProtocolModeExtraKeys = []string{
	"openai_responses_mode", "openai_compact_mode",
	"openai_apikey_responses_websockets_v2_mode", "openai_apikey_responses_websockets_v2_enabled",
	"responses_websockets_v2_enabled", "openai_ws_enabled",
	OpenAIPromptCacheKeyModeExtraKey,
}

// openAIAPIKeyProtocolModesForUpdate resolves the protocol-mode extras an
// update of an OpenAI API-key account writes: supplied values win, omitted
// keys retain their stored value, and a key in neither is left out. It
// returns nil for any other account.
func openAIAPIKeyProtocolModesForUpdate(account *Account, extra map[string]any) map[string]any {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return nil
	}
	modes := make(map[string]any, len(openAIAPIKeyProtocolModeExtraKeys))
	for _, key := range openAIAPIKeyProtocolModeExtraKeys {
		if value, present := extra[key]; present {
			modes[key] = value
		} else if value, present := account.Extra[key]; present {
			modes[key] = value
		}
	}
	return modes
}

// applyOpenAIAPIKeyProtocolModes gives the account's Extra exactly the resolved
// protocol-mode extras: each resolved key is written and every other
// protocol-mode key is removed. nil modes leave the account as it is.
func applyOpenAIAPIKeyProtocolModes(account *Account, modes map[string]any) {
	if modes == nil {
		return
	}
	account.Extra = maps.Clone(account.Extra)
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	for _, key := range openAIAPIKeyProtocolModeExtraKeys {
		if value, present := modes[key]; present {
			account.Extra[key] = value
		} else {
			delete(account.Extra, key)
		}
	}
}
