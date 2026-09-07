package service

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
)

// parsePositiveSafeModelContextTokens accepts JSON numbers only. Rational
// parsing avoids float rounding at the JavaScript safe-integer boundary and
// still accepts mathematically integral JSON forms such as 2.58e5 or 258000.0.
func parsePositiveSafeModelContextTokens(raw json.RawMessage) int64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 128 || (raw[0] < '0' || raw[0] > '9') || !json.Valid(raw) {
		return 0
	}
	// Bound exponents before math/big allocation; a valid capacity needs at most
	// sixteen decimal digits and no legitimate notation needs huge exponents.
	if exponent := bytes.IndexAny(raw, "eE"); exponent >= 0 {
		value, err := strconv.Atoi(string(raw[exponent+1:]))
		if err != nil || value < -128 || value > 128 {
			return 0
		}
	}
	value, ok := new(big.Rat).SetString(string(raw))
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0
	}
	tokens := value.Num().Int64()
	if !validModelContextTokens(tokens) {
		return 0
	}
	return tokens
}

// ParseUpstreamModelContextCapacity is intentionally field-tolerant. A bad
// context field cannot discard a valid output limit, other capabilities or the
// entire model ID. No max_tokens/request budget/usage/rate limit is a context.
func ParseUpstreamModelContextCapacity(raw json.RawMessage, platform string) ModelContextCapacity {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ModelContextCapacity{}
	}
	first := func(names ...string) int64 {
		for _, name := range names {
			if tokens := parsePositiveSafeModelContextTokens(fields[name]); tokens > 0 {
				return tokens
			}
		}
		return 0
	}
	value := ModelContextCapacity{
		ContextWindow:    first("context_window", "contextWindow", "context_length", "contextLength"),
		MaxContextWindow: first("max_context_window", "maxContextWindow"),
		MaxInputTokens:   first("max_input_tokens", "maxInputTokens", "input_token_limit", "inputTokenLimit"),
		MaxOutputTokens:  first("max_output_tokens", "maxOutputTokens", "output_token_limit", "outputTokenLimit"),
	}
	var limits map[string]json.RawMessage
	if json.Unmarshal(fields["limit"], &limits) == nil {
		if value.ContextWindow == 0 {
			value.ContextWindow = parsePositiveSafeModelContextTokens(limits["context"])
		}
		if value.MaxInputTokens == 0 {
			value.MaxInputTokens = parsePositiveSafeModelContextTokens(limits["input"])
		}
		if value.MaxOutputTokens == 0 {
			value.MaxOutputTokens = parsePositiveSafeModelContextTokens(limits["output"])
		}
	}
	// This field has an explicit output-limit meaning in Anthropic's Models
	// API. It is not standardized on arbitrary OpenAI-compatible catalogs.
	if platform == PlatformAnthropic && value.MaxOutputTokens == 0 {
		value.MaxOutputTokens = parsePositiveSafeModelContextTokens(fields["max_tokens"])
	}
	_ = json.Unmarshal(fields["observed_at"], &value.ObservedAt)
	_ = json.Unmarshal(fields["capacity_basis"], &value.CapacityBasis)
	value = sanitizeModelContextCapacity(value)
	if value.CapacityBasis == "" {
		switch {
		case value.ContextWindow > 0:
			value.CapacityBasis = ModelContextCapacityBasisTotal
		case value.MaxInputTokens > 0:
			value.CapacityBasis = ModelContextCapacityBasisInput
		case value.MaxContextWindow > 0:
			value.CapacityBasis = ModelContextCapacityBasisMaximum
		}
	}
	return value
}

// ParseUpstreamModelContextCapacities reads only the current response, before
// models.dev enrichment. The existing protocol ID selector is reused so raw
// capacities remain keyed by the same real upstream IDs as model sync.
func ParseUpstreamModelContextCapacities(body []byte, platform string) (map[string]ModelContextCapacity, error) {
	entries, err := extractUpstreamModelRawEntries(body)
	if err != nil {
		return nil, err
	}
	selectID := upstreamModelEntryID
	if platform == PlatformGrok {
		selectID = grokUpstreamModelEntryID
	}
	values := make(map[string]ModelContextCapacity)
	for _, raw := range entries {
		var idEntry upstreamModelEntry
		if json.Unmarshal(raw, &idEntry) != nil {
			continue
		}
		modelID := selectID(idEntry)
		if !validModelContextID(modelID) {
			continue
		}
		value := ParseUpstreamModelContextCapacity(raw, platform)
		if !modelContextCapacityHasLimits(value) {
			continue
		}
		// Duplicate entries cannot make model order choose a larger promise.
		// Merge only independently explicit fields and keep their minima.
		if previous, exists := values[modelID]; exists {
			value = mergeDuplicateModelContextCapacity(previous, value)
		}
		values[modelID] = value
	}
	return values, nil
}

func mergeDuplicateModelContextCapacity(left, right ModelContextCapacity) ModelContextCapacity {
	minimumKnown := func(a, b int64) int64 {
		if a <= 0 || (b > 0 && b < a) {
			return b
		}
		return a
	}
	left.ContextWindow = minimumKnown(left.ContextWindow, right.ContextWindow)
	left.MaxContextWindow = minimumKnown(left.MaxContextWindow, right.MaxContextWindow)
	left.MaxInputTokens = minimumKnown(left.MaxInputTokens, right.MaxInputTokens)
	left.MaxOutputTokens = minimumKnown(left.MaxOutputTokens, right.MaxOutputTokens)
	if left.CapacityBasis == "" {
		left.CapacityBasis = right.CapacityBasis
	}
	return left
}
