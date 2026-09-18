package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// requestIntegrityOptions tells the comparator which service-owned rewrites the
// hook's path applied, so the same non-lossy conversions can be replayed on the
// client body before the semantic fields are compared.
type requestIntegrityOptions struct {
	// UpstreamModel is the model the hook actually sends. The model field is
	// accepted when the wire value equals it or the account/channel mapping of
	// the client model.
	UpstreamModel string
	// Compact requests are excluded in v1: compact-only rewrites (store/stream
	// deletes, effort remaps) are not modelled, so the check is skipped.
	Compact bool
	// ResponsesLite replays the Responses-Lite request contract on the client body.
	ResponsesLite bool
	// Platform selects the tool-schema repairs (defaults to the account platform).
	Platform string
	// FlattenNamespaces / StripInputNamespaces replay the namespace rewrites
	// Forward applied; the keep flags mirror the strip call's arguments.
	FlattenNamespaces              bool
	StripInputNamespaces           bool
	KeepToolCallNamespaces         bool
	KeepStandaloneOutputNamespaces bool
	// ImageToolPolicyStrip: the account policy removed client image_generation
	// tools (an intentional, admin-owned change). Spark models imply the same.
	ImageToolPolicyStrip bool
	// ImageBridgeEnabled: the Codex image bridge may inject an image_generation
	// tool that the client did not declare.
	ImageBridgeEnabled bool
	// EffortPolicy replays the reasoning-effort alias materialization and group
	// policy on the raw client body (HTTP: materializeOpenAIForwardReasoningEffort
	// with the same model candidates as Forward; WS: applyOpenAIWSReasoningEffortPolicy).
	EffortPolicy func([]byte) ([]byte, error)
}

// requestIntegrityComparedKeys are compared in this order; only keys present in
// the client body participate and the first difference is reported.
var requestIntegrityComparedKeys = []string{
	"model",
	"input",
	"instructions",
	"reasoning",
	"tools",
	"tool_choice",
	"parallel_tool_calls",
	"text",
	"previous_response_id",
	"max_output_tokens",
	"max_completion_tokens",
}

// compareRequestIntegrity returns the first compared key whose canonical value
// differs between the client body (original) and the wire body (final), or ""
// when the transform was lossless. The error is only for undecodable input.
//
// The lossy transforms this deliberately does not hide: orphan tool-output
// removal, dropped nameless tools, tool_choice forced onto an undeclared tool,
// role:tool messages without a call id, empty base64 images, and reasoning
// items removed by recovery retries.
func compareRequestIntegrity(account *Account, opts requestIntegrityOptions, original, final []byte) (string, error) {
	beforeRaw, err := requestIntegrityRawCanonical(account, opts, original, true)
	if err != nil {
		return "", err
	}
	afterRaw, err := requestIntegrityRawCanonical(account, opts, final, false)
	if err != nil {
		return "", err
	}
	before, err := decodeRequestIntegrityObject(beforeRaw)
	if err != nil {
		return "", err
	}
	after, err := decodeRequestIntegrityObject(afterRaw)
	if err != nil {
		return "", err
	}

	// The ChatGPT Codex endpoint does not accept token budgets; their absence on
	// the wire is a protocol limitation, not a lost field.
	for _, key := range []string{"max_output_tokens", "max_completion_tokens"} {
		if _, exists := after[key]; !exists {
			delete(before, key)
		}
	}
	clientModel := strings.TrimSpace(firstNonEmptyString(before["model"]))

	canonicalizeRequestIntegrityBody(before, opts)
	canonicalizeRequestIntegrityBody(after, opts)

	acceptRequestIntegrityModelMapping(account, opts, before, after, clientModel)
	relaxRequestIntegrityInstructions(before, after)
	relaxRequestIntegrityTools(opts, before, after)

	normalizeRequestIntegrityNumbers(before)
	normalizeRequestIntegrityNumbers(after)
	for _, key := range requestIntegrityComparedKeys {
		value, exists := before[key]
		if !exists {
			continue
		}
		if !reflect.DeepEqual(value, after[key]) {
			return key, nil
		}
	}
	return "", nil
}

// requestIntegrityRawCanonical replays the byte-level compatibility passes in
// the order Forward applies them. Passes that only the client body needs
// (effort policy, Lite, namespaces) run on the original; idempotent passes run
// on both sides so a body that already carries them compares equal.
func requestIntegrityRawCanonical(account *Account, opts requestIntegrityOptions, body []byte, original bool) ([]byte, error) {
	if original && opts.EffortPolicy != nil {
		next, err := opts.EffortPolicy(body)
		if err != nil {
			return nil, err
		}
		body = next
	}
	next, _, err := normalizeOpenAIResponsesLegacyIngress(body)
	if err != nil {
		return nil, err
	}
	body = next
	platform := strings.TrimSpace(opts.Platform)
	if platform == "" && account != nil {
		platform = account.Platform
	}
	next, _, err = sanitizeOpenAIResponsesToolSchemasForPlatform(body, platform)
	if err != nil {
		return nil, err
	}
	body = next
	if original {
		if opts.ResponsesLite {
			next, _, err = normalizeOpenAIResponsesLitePayloadForAccount(body, account)
			if err != nil {
				return nil, err
			}
			body = next
		}
		if opts.FlattenNamespaces {
			// A nil gin context keeps the replay from publishing namespace names.
			next, err = flattenOpenAIResponsesNamespaces(nil, body)
			if err != nil {
				return nil, err
			}
			body = next
		}
		if opts.StripInputNamespaces {
			next, err = stripOpenAIResponsesInputNamespaces(body, opts.KeepToolCallNamespaces, opts.KeepStandaloneOutputNamespaces)
			if err != nil {
				return nil, err
			}
			body = next
		}
	}
	next, _, err = NormalizeCompactionTriggerInputOrder(body)
	if err != nil {
		return nil, err
	}
	return next, nil
}

func decodeRequestIntegrityObject(raw []byte) (map[string]any, error) {
	var body map[string]any
	if err := decodeOpenAIJSONUseNumber(raw, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	return body, nil
}

// canonicalizeRequestIntegrityBody applies the documented equivalences of the
// Codex OAuth transform to one decoded body. It is a small equivalence map, not
// a second forwarding pipeline: unknown fields, item types and content stay
// comparable, and lossy conversions are intentionally absent.
func canonicalizeRequestIntegrityBody(body map[string]any, opts requestIntegrityOptions) {
	if body == nil {
		return
	}
	// Model alias (gpt-5.4-high -> gpt-5.4 + reasoning.effort) and whitespace.
	applyOpenAIModelReasoningAlias(body)
	if model, ok := body["model"].(string); ok {
		body["model"] = strings.TrimSpace(model)
	}
	// prompt -> input, commands, internal Codex message metadata.
	normalizeOpenAIOAuthResponsesCompatibilityFields(body)

	resolvedModel := strings.TrimSpace(opts.UpstreamModel)
	if resolvedModel == "" {
		resolvedModel, _ = body["model"].(string)
	}
	canonicalizeRequestIntegrityReasoningMode(body, resolvedModel)

	// The Codex endpoint requires input to be an item list.
	switch input := body["input"].(type) {
	case string:
		if strings.TrimSpace(input) != "" {
			body["input"] = []any{map[string]any{"type": "message", "role": "user", "content": input}}
		} else {
			body["input"] = []any{}
		}
	case map[string]any:
		body["input"] = []any{input}
	}

	// Text-only system messages are promoted into instructions; JSON object
	// mode keeps them in input as developer items.
	omitSystem := true
	if text, ok := body["text"].(map[string]any); ok {
		if format, ok := text["format"].(map[string]any); ok &&
			strings.EqualFold(strings.TrimSpace(firstNonEmptyString(format["type"])), "json_object") {
			omitSystem = false
		}
	}
	extractSystemMessagesFromInput(body, omitSystem)
	if isInstructionsEmpty(body) {
		// An empty instructions field may receive service defaults.
		delete(body, "instructions")
	}

	canonicalizeRequestIntegrityLegacyTools(body)
	if resolvedModel != "" && !SupportsVerbosity(resolvedModel) {
		if text, ok := body["text"].(map[string]any); ok {
			delete(text, "verbosity")
		}
	}
	normalizeOpenAIResponseFormatSchemas(body)
	normalizeOpenAIResponsesImageGenerationTools(body)
	// Reserved tool-name aliasing is bijective and applied to declarations,
	// choices and call items together; an error leaves the body comparable.
	_, _, _ = aliasOpenAIOAuthReservedToolNames(body)

	canonicalizeRequestIntegrityInput(body)
}

// canonicalizeRequestIntegrityReasoningMode mirrors
// normalizeOpenAIResponsesReasoningModeForModel: outside Astra, reasoning.mode
// is dropped and mode=pro without an effort becomes effort=max.
func canonicalizeRequestIntegrityReasoningMode(body map[string]any, resolvedModel string) {
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || isOpenAIGPT6AstraModel(resolvedModel) {
		return
	}
	mode, ok := reasoning["mode"].(string)
	if !ok {
		return
	}
	effort, hasEffort := reasoning["effort"]
	effortEmpty := !hasEffort || effort == nil
	if value, isString := effort.(string); isString && strings.TrimSpace(value) == "" {
		effortEmpty = true
	}
	if effortEmpty && strings.EqualFold(strings.TrimSpace(mode), "pro") {
		reasoning["effort"] = "max"
	}
	delete(reasoning, "mode")
	if len(reasoning) == 0 {
		delete(body, "reasoning")
	}
}

// canonicalizeRequestIntegrityLegacyTools folds the Chat Completions tool shapes
// into their Responses equivalents: functions -> tools, function_call ->
// tool_choice, and nested function objects onto the tool / tool_choice itself.
// A body carrying both the legacy and the native field keeps both (that
// conflict is a semantic change, not an equivalence).
func canonicalizeRequestIntegrityLegacyTools(body map[string]any) {
	if functions, ok := body["functions"].([]any); ok {
		if _, hasTools := body["tools"]; !hasTools {
			tools := make([]any, 0, len(functions))
			for _, function := range functions {
				tools = append(tools, map[string]any{"type": "function", "function": function})
			}
			body["tools"] = tools
			delete(body, "functions")
		}
	}
	if choice, exists := body["function_call"]; exists {
		if _, hasChoice := body["tool_choice"]; !hasChoice {
			switch value := choice.(type) {
			case string:
				body["tool_choice"] = value
				delete(body, "function_call")
			case map[string]any:
				if name := strings.TrimSpace(firstNonEmptyString(value["name"])); name != "" {
					body["tool_choice"] = map[string]any{"type": "function", "name": name}
					delete(body, "function_call")
				}
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok || strings.TrimSpace(firstNonEmptyString(tool["type"])) != "function" {
				continue
			}
			function, ok := tool["function"].(map[string]any)
			if !ok {
				continue
			}
			for key, value := range function {
				if _, exists := tool[key]; !exists {
					tool[key] = value
				}
			}
			delete(tool, "function")
		}
	}
	if choice, ok := body["tool_choice"].(map[string]any); ok &&
		strings.TrimSpace(firstNonEmptyString(choice["type"])) == "function" {
		if function, ok := choice["function"].(map[string]any); ok {
			if _, exists := choice["name"]; !exists {
				choice["name"] = function["name"]
			}
			delete(choice, "function")
		}
	}
}

// canonicalizeRequestIntegrityInput mirrors the per-item equivalences of
// filterCodexInputWithOptions, sanitizeOpenAIResponsesInputItemIDs and
// normalizeOpenAIResponsesReasoningContentReplay: replay metadata (message and
// reasoning ids, non-pair call ids, visible reasoning content) is removed,
// call ids take the fc_/ctc_/tsc_ form, and content is normalized to parts.
// Item order, content and every other field remain protected.
func canonicalizeRequestIntegrityInput(body map[string]any) {
	input, ok := body["input"].([]any)
	if !ok {
		return
	}
	// Outside a tool continuation the OAuth transform strips every replayed
	// item id; the signal depends only on compared fields, so both sides agree.
	preserveIDs := NeedsToolContinuation(body)
	referenceIDs := codexItemReferenceIDMappings(input, false)
	itemIDs := codexInputItemIDs(input)
	callIDs := codexInputCallIDs(input)
	for index, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := item["role"].(string)
		role = strings.TrimSpace(role)
		if role == "tool" && strings.TrimSpace(firstNonEmptyString(item["call_id"], item["tool_call_id"], item["id"])) != "" {
			// role:tool with a call id converts losslessly to function_call_output.
			if _, lossless := extractLosslessTextFromContent(item["content"]); lossless {
				if converted, changed := normalizeCodexToolRoleMessages([]any{item}); changed {
					if next, ok := converted[0].(map[string]any); ok {
						item = next
						input[index] = item
					}
				}
			}
		}
		typ, _ := item["type"].(string)
		typ = strings.TrimSpace(typ)
		if typ == "" {
			switch role {
			case "user", "assistant", "developer":
				typ = "message"
				item["type"] = typ
			}
		}
		if typ != "item_reference" && !isCodexToolCallItemType(typ) {
			delete(item, "call_id")
		}
		switch typ {
		case "message":
			// Message ids are store=false replay metadata; role, content and
			// part order stay protected.
			delete(item, "id")
			if text, ok := item["content"].(string); ok {
				item["content"] = []any{map[string]any{"type": "input_text", "text": text}}
			}
			if parts, ok := item["content"].([]any); ok {
				for _, rawPart := range parts {
					part, ok := rawPart.(map[string]any)
					if !ok {
						continue
					}
					switch part["type"] {
					case "text", "output_text":
						part["type"] = "input_text"
					}
					if text, hasText := part["text"]; hasText {
						if _, isString := text.(string); !isString {
							part["text"] = stringifyCodexContentText(text)
						}
					}
				}
			}
		case "reasoning":
			delete(item, "id")
			if content, ok := item["content"].([]any); ok && len(content) > 0 {
				// Visible reasoning content is not replayable; encrypted_content is.
				delete(item, "content")
			}
			if summary, ok := item["summary"]; !ok || summary == nil {
				item["summary"] = []any{}
			}
		case "compaction_summary":
			delete(item, "id")
		case "item_reference":
			id, _ := item["id"].(string)
			id = strings.TrimSpace(id)
			if _, existing := itemIDs[id]; !existing && strings.HasPrefix(id, "call_") {
				if mapped, exists := referenceIDs[id]; exists {
					item["id"] = mapped
				} else if _, sameTurnCall := callIDs[id]; !sameTurnCall {
					item["id"] = normalizeCodexCallID(id)
				}
			}
		default:
			if isCodexToolCallItemType(typ) {
				if id := strings.TrimSpace(firstNonEmptyString(item["call_id"], item["id"])); id != "" {
					item["call_id"] = normalizeCodexCallIDForItemType(typ, id)
				}
				delete(item, "id")
				if codexInputItemRequiresName(typ) && strings.TrimSpace(firstNonEmptyString(item["name"])) == "" {
					name := firstNonEmptyString(item["tool_name"])
					if name == "" {
						if function, ok := item["function"].(map[string]any); ok {
							name = firstNonEmptyString(function["name"])
						}
					}
					if name == "" {
						name = "tool"
					}
					item["name"] = name
				}
			} else if id, ok := item["id"].(string); ok && (!preserveIDs || shouldStripOpenAIResponsesInputItemID(typ, id)) {
				delete(item, "id")
			}
		}
	}
}

// acceptRequestIntegrityModelMapping accepts the wire model when it is the
// model the hook resolved for this attempt or the account/channel mapping of
// the client model (before or after alias resolution).
func acceptRequestIntegrityModelMapping(account *Account, opts requestIntegrityOptions, before, after map[string]any, clientModel string) {
	actual, ok := after["model"].(string)
	if !ok {
		return
	}
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return
	}
	accepted := actual == strings.TrimSpace(opts.UpstreamModel)
	if !accepted && account != nil {
		canonicalModel := strings.TrimSpace(firstNonEmptyString(before["model"]))
		for _, candidate := range []string{clientModel, canonicalModel} {
			if candidate != "" && actual == normalizeOpenAIModelForUpstream(account, account.GetMappedModel(candidate)) {
				accepted = true
				break
			}
		}
	}
	if _, exists := before["model"]; accepted && exists {
		before["model"] = actual
	}
}

// relaxRequestIntegrityInstructions tolerates the service-owned suffixes the
// gateway appends to client instructions (default Codex instructions after
// system promotion, business system prompt, image-bridge and Spark notes): a
// wire value that starts with the canonical client instructions is accepted.
// Without client instructions any wire value is accepted because the key is
// then absent from the compared set.
func relaxRequestIntegrityInstructions(before, after map[string]any) {
	expected, ok := before["instructions"].(string)
	if !ok || strings.TrimSpace(expected) == "" {
		return
	}
	if actual, ok := after["instructions"].(string); ok && strings.HasPrefix(actual, expected) {
		after["instructions"] = expected
	}
}

// relaxRequestIntegrityTools accepts the intentional image_generation tool
// edits: admin strip policy and Spark rejection remove the tool on both sides,
// and the Codex image bridge may add one the client did not declare.
func relaxRequestIntegrityTools(opts requestIntegrityOptions, before, after map[string]any) {
	if opts.ImageToolPolicyStrip || isCodexSparkModel(opts.UpstreamModel) {
		stripOpenAIImageGenerationTools(before)
		stripOpenAIImageGenerationTools(after)
	}
	if opts.ImageBridgeEnabled && !hasOpenAIImageGenerationTool(before) {
		if tools, ok := after["tools"].([]any); ok {
			kept := make([]any, 0, len(tools))
			for _, raw := range tools {
				if tool, ok := raw.(map[string]any); ok &&
					strings.TrimSpace(firstNonEmptyString(tool["type"])) == "image_generation" {
					continue
				}
				kept = append(kept, raw)
			}
			after["tools"] = kept
		}
	}
}

// normalizeRequestIntegrityNumbers compares numbers by value: some passes
// decode through float64 and re-encode (1.0 -> 1), which is not a semantic change.
func normalizeRequestIntegrityNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeRequestIntegrityNumbers(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = normalizeRequestIntegrityNumbers(item)
		}
		return typed
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return parsed
		}
		return string(typed)
	default:
		return value
	}
}
