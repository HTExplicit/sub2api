package service

import (
	"crypto/rand"
	"encoding/hex"
	"maps"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	CindyDeviceIDExtraKey       = "cindy_device_id"
	CindyDeviceIDSourceExtraKey = "cindy_device_id_source"
	CindyResponsesModeExtraKey  = "openai_responses_mode"
	CindyAlphaSearchExtraKey    = "openai_alpha_search_mode"
	CindyPromptCacheExtraKey    = "openai_prompt_cache_key_mode"
	cindyAPIHost                = "api.laxarouter.ai"
)

var cindyDeviceIDSources = map[string]struct{}{
	"registration-record":     {},
	"input-preserved":         {},
	"generated-local-v1":      {},
	"generated-production-v1": {},
}

func IsCindyAPIKeyAccount(platform, accountType string, credentials map[string]any) bool {
	if platform != PlatformOpenAI || accountType != AccountTypeAPIKey || credentials == nil {
		return false
	}
	rawBaseURL, ok := credentials["base_url"].(string)
	if !ok {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), cindyAPIHost) {
		return false
	}
	return parsed.Port() == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
		(parsed.EscapedPath() == "" || parsed.EscapedPath() == "/")
}

func normalizeCindyDeviceID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) == 64 {
		for _, character := range value {
			if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
				return "", false
			}
		}
		return value, true
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", false
	}
	canonical := parsed.String()
	if canonical != strings.ToLower(value) {
		return "", false
	}
	return canonical, true
}

func ValidCindyDeviceID(value string) bool {
	_, ok := normalizeCindyDeviceID(value)
	return ok
}

func generateCindyDeviceID() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func normalizeCindyDeviceIDSource(value any) (string, bool) {
	source, ok := value.(string)
	if !ok {
		return "", false
	}
	source = strings.TrimSpace(source)
	_, ok = cindyDeviceIDSources[source]
	return source, ok
}

// NormalizeCindyDeviceIdentityExtra validates and completes the store-only Cindy identity.
// It does not imply or perform any upstream request-header injection.
func NormalizeCindyDeviceIdentityExtra(
	platform string,
	accountType string,
	credentials map[string]any,
	requested map[string]any,
	current map[string]any,
) (map[string]any, error) {
	if !IsCindyAPIKeyAccount(platform, accountType, credentials) {
		return requested, nil
	}

	normalized := maps.Clone(requested)
	if normalized == nil {
		normalized = make(map[string]any, 2)
	}

	currentID := ""
	if rawCurrentID, currentPresent := current[CindyDeviceIDExtraKey]; currentPresent {
		storedID, ok := rawCurrentID.(string)
		if !ok {
			return nil, infraerrors.BadRequest("CINDY_DEVICE_ID_INVALID", "stored cindy_device_id is invalid")
		}
		var valid bool
		currentID, valid = normalizeCindyDeviceID(storedID)
		if !valid {
			return nil, infraerrors.BadRequest("CINDY_DEVICE_ID_INVALID", "stored cindy_device_id is invalid")
		}
	}

	rawRequestedID, requestedIDPresent := normalized[CindyDeviceIDExtraKey]
	var deviceID string
	if requestedIDPresent {
		requestedID, ok := rawRequestedID.(string)
		if !ok {
			return nil, infraerrors.BadRequest("CINDY_DEVICE_ID_INVALID", "cindy_device_id must be a UUID or 64 lowercase hexadecimal characters")
		}
		if currentID != "" && requestedID == MaskCindyDeviceID(currentID) {
			deviceID = currentID
			requestedIDPresent = false
		} else {
			var valid bool
			deviceID, valid = normalizeCindyDeviceID(requestedID)
			if !valid {
				return nil, infraerrors.BadRequest("CINDY_DEVICE_ID_INVALID", "cindy_device_id must be a UUID or 64 lowercase hexadecimal characters")
			}
		}
	} else if currentID != "" {
		deviceID = currentID
	} else {
		generated, err := generateCindyDeviceID()
		if err != nil {
			return nil, err
		}
		deviceID = generated
	}

	source := ""
	if rawSource, sourcePresent := normalized[CindyDeviceIDSourceExtraKey]; sourcePresent {
		var valid bool
		source, valid = normalizeCindyDeviceIDSource(rawSource)
		if !valid {
			return nil, infraerrors.BadRequest("CINDY_DEVICE_ID_SOURCE_INVALID", "cindy_device_id_source is invalid")
		}
	} else if rawCurrentSource, currentSourcePresent := current[CindyDeviceIDSourceExtraKey]; currentSourcePresent {
		currentSource, valid := normalizeCindyDeviceIDSource(rawCurrentSource)
		if !valid {
			return nil, infraerrors.BadRequest("CINDY_DEVICE_ID_SOURCE_INVALID", "stored cindy_device_id_source is invalid")
		}
		if !requestedIDPresent || currentID == deviceID {
			source = currentSource
		}
	}
	if source == "" {
		if requestedIDPresent {
			source = "input-preserved"
		} else {
			source = "generated-production-v1"
		}
	}

	normalized[CindyDeviceIDExtraKey] = deviceID
	normalized[CindyDeviceIDSourceExtraKey] = source
	setCindyDefault(normalized, current, CindyResponsesModeExtraKey, "force_responses")
	setCindyDefault(normalized, current, CindyAlphaSearchExtraKey, OpenAIAlphaSearchModeResponsesWebSearch)
	setCindyDefault(normalized, current, CindyPromptCacheExtraKey, OpenAIPromptCacheKeyModeSHA25664)
	return normalized, nil
}

func setCindyDefault(normalized, current map[string]any, key string, fallback any) {
	if _, present := normalized[key]; present {
		return
	}
	if value, present := current[key]; present {
		normalized[key] = value
		return
	}
	normalized[key] = fallback
}

func MaskCindyDeviceID(value string) string {
	normalized, ok := normalizeCindyDeviceID(value)
	if !ok {
		return ""
	}
	if len(normalized) == 64 {
		return normalized[:8] + "..." + normalized[len(normalized)-8:]
	}
	return normalized[:8] + "..." + normalized[len(normalized)-4:]
}
