package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

const (
	promptCacheDiagnosticMaxBody  = 8 << 20
	promptCacheDiagnosticMaxItems = 2048
	promptCacheDiagnosticMaxNodes = 65536
)

type promptCacheFieldFingerprint struct {
	Present bool   `json:"present"`
	Bytes   int    `json:"bytes,omitempty"`
	Hash    string `json:"hash,omitempty"`
}

type promptCacheInputFingerprint struct {
	Type       string
	Role       string
	Hash       string
	Envelope   string
	Content    string
	Cipher     string
	Extensions string
}

type promptCacheSnapshot struct {
	Source        string                                 `json:"source"`
	Status        string                                 `json:"status"`
	BodyBytes     int                                    `json:"body_bytes"`
	BodyHash      string                                 `json:"body_hash,omitempty"`
	InputCount    int                                    `json:"input_count"`
	InputKind     string                                 `json:"input_kind"`
	InputHash     string                                 `json:"input_hash,omitempty"`
	CacheKey      promptCacheFieldFingerprint            `json:"cache_key"`
	Fields        map[string]promptCacheFieldFingerprint `json:"fields,omitempty"`
	UnknownFields promptCacheFieldFingerprint            `json:"unknown_fields"`
	items         []promptCacheInputFingerprint
}

type promptCacheComparison struct {
	BaselineRequest int      `json:"baseline_request,omitempty"`
	Comparable      bool     `json:"comparable"`
	CacheKeyEqual   bool     `json:"cache_key_equal"`
	ChangedFields   []string `json:"changed_fields,omitempty"`
	UnknownEqual    bool     `json:"unknown_fields_equal"`
	InputKindEqual  bool     `json:"input_kind_equal"`
	InputEqual      bool     `json:"input_equal"`
	AppendOnly      bool     `json:"input_append_only"`
	CommonItems     int      `json:"common_input_items"`
	PreviousItems   int      `json:"previous_input_items"`
	CurrentItems    int      `json:"current_input_items"`
	FirstDifference *int     `json:"first_changed_item,omitempty"`
	ChangedParts    []string `json:"changed_item_parts,omitempty"`
	ChangedItemType string   `json:"changed_item_type,omitempty"`
	ChangedItemRole string   `json:"changed_item_role,omitempty"`
}

var promptCacheDiagnosticFields = []string{
	"model", "instructions", "tools", "reasoning", "text", "tool_choice", "parallel_tool_calls",
	"include", "prompt", "conversation", "previous_response_id", "prompt_cache_options",
	"prompt_cache_retention", "store", "stream", "max_output_tokens", "truncation",
	"client_metadata", "metadata", "service_tier", "compaction_trigger",
}

func promptCacheDigest(key []byte, data []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func promptCacheValueFingerprint(key []byte, value any, present bool) promptCacheFieldFingerprint {
	if !present {
		return promptCacheFieldFingerprint{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return promptCacheFieldFingerprint{Present: true}
	}
	return promptCacheFieldFingerprint{Present: true, Bytes: len(encoded), Hash: promptCacheDigest(key, encoded)}
}

// Decode only within a strict depth/node/byte budget. Duplicate keys make a
// snapshot ambiguous, so diagnostics decline them without affecting inference.
func promptCacheDecodeValue(dec *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > 64 || *nodes > promptCacheDiagnosticMaxNodes {
		return nil, errors.New("diagnostic JSON budget exceeded")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delim {
	case '{':
		result := make(map[string]any)
		for dec.More() {
			name, nameErr := dec.Token()
			if nameErr != nil {
				return nil, nameErr
			}
			field, ok := name.(string)
			if !ok {
				return nil, errors.New("invalid diagnostic JSON key")
			}
			if _, exists := result[field]; exists {
				return nil, errors.New("duplicate diagnostic JSON key")
			}
			value, valueErr := promptCacheDecodeValue(dec, depth+1, nodes)
			if valueErr != nil {
				return nil, valueErr
			}
			result[field] = value
		}
		_, err = dec.Token()
		return result, err
	case '[':
		result := make([]any, 0)
		for dec.More() {
			value, valueErr := promptCacheDecodeValue(dec, depth+1, nodes)
			if valueErr != nil {
				return nil, valueErr
			}
			result = append(result, value)
		}
		_, err = dec.Token()
		return result, err
	default:
		return nil, errors.New("invalid diagnostic JSON delimiter")
	}
}

func buildPromptCacheSnapshot(key, body []byte, source string) *promptCacheSnapshot {
	s := &promptCacheSnapshot{Source: source, Status: "unavailable", BodyBytes: len(body), InputKind: "missing"}
	if len(body) > promptCacheDiagnosticMaxBody {
		s.Status = "body_limit"
		return s
	}
	s.BodyHash = promptCacheDigest(key, body)
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	nodes := 0
	value, err := promptCacheDecodeValue(dec, 0, &nodes)
	if err != nil {
		s.Status = "invalid_or_limited_json"
		return s
	}
	if _, err = dec.Token(); err != io.EOF {
		s.Status = "invalid_or_limited_json"
		return s
	}
	root, ok := value.(map[string]any)
	if !ok {
		s.Status = "not_object"
		return s
	}
	s.Status = "complete"
	s.Fields = make(map[string]promptCacheFieldFingerprint, len(promptCacheDiagnosticFields))
	for _, name := range promptCacheDiagnosticFields {
		v, exists := root[name]
		s.Fields[name] = promptCacheValueFingerprint(key, v, exists)
		delete(root, name)
	}
	cacheKey, exists := root["prompt_cache_key"]
	s.CacheKey = promptCacheValueFingerprint(key, cacheKey, exists)
	delete(root, "prompt_cache_key")
	input, exists := root["input"]
	delete(root, "input")
	s.UnknownFields = promptCacheValueFingerprint(key, root, len(root) != 0)
	if !exists {
		return s
	}
	s.InputHash = promptCacheValueFingerprint(key, input, true).Hash
	items, array := input.([]any)
	if !array {
		s.InputKind = "non_array"
		return s
	}
	s.InputKind = "array"
	s.InputCount = len(items)
	if len(items) > promptCacheDiagnosticMaxItems {
		s.Status = "input_item_limit"
		return s
	}
	s.items = make([]promptCacheInputFingerprint, 0, len(items))
	for _, item := range items {
		fp := promptCacheInputFingerprint{Hash: promptCacheValueFingerprint(key, item, true).Hash}
		if fields, object := item.(map[string]any); object {
			fp.Type = promptCacheSafeItemLabel(fields["type"])
			fp.Role = promptCacheSafeItemLabel(fields["role"])
			envelope := make(map[string]any)
			content := make(map[string]any)
			extensions := make(map[string]any)
			for name, field := range fields {
				switch name {
				case "type", "role", "id", "call_id", "name", "namespace", "status", "phase":
					envelope[name] = field
				case "content", "output", "arguments", "summary":
					content[name] = field
				case "encrypted_content":
					fp.Cipher = promptCacheValueFingerprint(key, field, true).Hash
				default:
					extensions[name] = field
				}
			}
			fp.Envelope = promptCacheValueFingerprint(key, envelope, true).Hash
			fp.Content = promptCacheValueFingerprint(key, content, true).Hash
			fp.Extensions = promptCacheValueFingerprint(key, extensions, true).Hash
		}
		s.items = append(s.items, fp)
	}
	return s
}

func promptCacheSafeItemLabel(value any) string {
	s, _ := value.(string)
	switch s {
	case "", "message", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output",
		"user", "assistant", "system", "developer", "tool", "item_reference", "configuration_update", "compaction":
		return s
	default:
		return "other"
	}
}

func comparePromptCacheSnapshots(previous, current *promptCacheSnapshot, baseline int) *promptCacheComparison {
	d := &promptCacheComparison{BaselineRequest: baseline}
	if previous == nil || current == nil || previous.Status != "complete" || current.Status != "complete" {
		return d
	}
	d.Comparable = true
	d.CacheKeyEqual = previous.CacheKey == current.CacheKey
	d.UnknownEqual = previous.UnknownFields == current.UnknownFields
	for _, name := range promptCacheDiagnosticFields {
		if previous.Fields[name] != current.Fields[name] {
			d.ChangedFields = append(d.ChangedFields, name)
		}
	}
	sort.Strings(d.ChangedFields)
	d.InputKindEqual = previous.InputKind == current.InputKind
	d.InputEqual = previous.InputHash == current.InputHash
	d.PreviousItems, d.CurrentItems = previous.InputCount, current.InputCount
	if previous.InputKind != "array" || current.InputKind != "array" {
		return d
	}
	for d.CommonItems < len(previous.items) && d.CommonItems < len(current.items) && previous.items[d.CommonItems].Hash == current.items[d.CommonItems].Hash {
		d.CommonItems++
	}
	d.AppendOnly = d.CommonItems == len(previous.items) && len(current.items) >= len(previous.items)
	if !d.AppendOnly {
		index := d.CommonItems
		d.FirstDifference = &index
		if index < len(previous.items) && index < len(current.items) {
			a, b := previous.items[index], current.items[index]
			d.ChangedItemType, d.ChangedItemRole = b.Type, b.Role
			for _, part := range []struct{ name, a, b string }{
				{"envelope", a.Envelope, b.Envelope}, {"content", a.Content, b.Content},
				{"encrypted_content", a.Cipher, b.Cipher}, {"extensions", a.Extensions, b.Extensions},
			} {
				if part.a != part.b {
					d.ChangedParts = append(d.ChangedParts, part.name)
				}
			}
		}
	}
	return d
}
