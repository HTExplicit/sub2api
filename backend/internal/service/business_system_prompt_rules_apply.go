package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var ErrPromptDeliveryUnsupported = errors.New("prompt_delivery_unsupported")

func promptRuleFieldHash(raw []byte) [32]byte {
	if len(raw) == 0 {
		return sha256.Sum256(nil)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			return sha256.Sum256(encoded)
		}
	}
	return sha256.Sum256(raw)
}

func applyPromptRules(body []byte, application BusinessSystemPromptApplication) ([]byte, BusinessSystemPromptApplication, error) {
	if application.RulesPlan == nil || !application.Applied {
		return body, application, nil
	}
	if !json.Valid(body) {
		return nil, application, ErrBusinessSystemPromptInvalid
	}
	groups := map[string][]extensionv1.PromptRulePlacement{}
	for _, placement := range application.RulesPlan.Placements {
		groups[placement.Carrier] = append(groups[placement.Carrier], placement)
	}
	out := body
	for _, field := range []string{"instructions", "input", "messages"} {
		placements := groups[field]
		if len(placements) == 0 {
			continue
		}
		value := gjson.GetBytes(out, field)
		if field == "instructions" {
			if value.Exists() && value.Type != gjson.String {
				return nil, application, fmt.Errorf("%w: instructions must be a string", ErrBusinessSystemPromptInvalid)
			}
			application.OriginalInstructionsExists = value.Exists()
			application.ClientInstructions = value.String()
			before, after := []string{}, []string{}
			publicBefore, publicAfter := []string{}, []string{}
			for _, placement := range placements {
				switch placement.Position {
				case extensionv1.PromptPositionControlPrepend:
					before = append(before, placement.Body)
					if placement.PreserveEcho {
						publicBefore = append(publicBefore, placement.Body)
					}
				case extensionv1.PromptPositionControlAppend:
					after = append(after, placement.Body)
					if placement.PreserveEcho {
						publicAfter = append(publicAfter, placement.Body)
					}
				default:
					return nil, application, ErrPromptDeliveryUnsupported
				}
			}
			parts := append(before, strings.TrimSpace(value.String()))
			parts = append(parts, after...)
			parts = nonEmptyPromptParts(parts)
			application.FinalInstructions = strings.Join(parts, "\n\n")
			application.PublicInstructions, application.PublicInstructionsExists = value.String(), value.Exists()
			if len(publicBefore)+len(publicAfter) > 0 {
				publicParts := append(publicBefore, strings.TrimSpace(value.String()))
				publicParts = append(publicParts, publicAfter...)
				application.PublicInstructions, application.PublicInstructionsExists = strings.Join(nonEmptyPromptParts(publicParts), "\n\n"), true
			}
			var err error
			out, err = sjson.SetBytes(out, field, application.FinalInstructions)
			if err != nil {
				return nil, application, err
			}
			continue
		}
		items := []json.RawMessage{}
		if field == "input" && value.Type == gjson.String {
			message, _ := json.Marshal(map[string]any{"role": "user", "content": value.String()})
			items = append(items, message)
		} else if value.Exists() {
			if !value.IsArray() || json.Unmarshal([]byte(value.Raw), &items) != nil {
				return nil, application, fmt.Errorf("%w: %s must be a message array", ErrBusinessSystemPromptInvalid, field)
			}
		} else if field == "messages" {
			return nil, application, fmt.Errorf("%w: messages must be an array", ErrBusinessSystemPromptInvalid)
		}
		controlEnd := 0
		for controlEnd < len(items) {
			role := gjson.GetBytes(items[controlEnd], "role").String()
			if role != "system" && role != "developer" {
				break
			}
			controlEnd++
		}
		insertions := map[int][]json.RawMessage{}
		for _, placement := range placements {
			if placement.Role != "system" && placement.Role != "developer" {
				return nil, application, ErrPromptDeliveryUnsupported
			}
			index := 0
			switch placement.Position {
			case extensionv1.PromptPositionControlPrepend, extensionv1.PromptPositionConversationHead:
			case extensionv1.PromptPositionControlAppend:
				index = controlEnd
			case extensionv1.PromptPositionConversationTail:
				index = len(items)
			default:
				return nil, application, ErrPromptDeliveryUnsupported
			}
			message, _ := json.Marshal(map[string]string{"role": placement.Role, "content": placement.Body})
			insertions[index] = append(insertions[index], message)
		}
		combined := make([]json.RawMessage, 0, len(items)+len(placements))
		for index := 0; index <= len(items); index++ {
			combined = append(combined, insertions[index]...)
			if index < len(items) {
				combined = append(combined, items[index])
			}
		}
		raw, err := json.Marshal(combined)
		if err != nil {
			return nil, application, err
		}
		out, err = sjson.SetRawBytes(out, field, raw)
		if err != nil {
			return nil, application, err
		}
	}
	return out, application, nil
}

func nonEmptyPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Proofs retain only insertion indices and bounded scalar control fields.
// Large customer arrays are hashed, never retained as another undo copy.
type promptRulesCarrierUndo struct {
	field        string
	beforeExists bool
	beforeHash   [32]byte
	afterHash    [32]byte
	scalar       []byte
	indices      []int
	valid        bool
	restorable   bool
}

func uniquePromptField(body []byte, field string) (gjson.Result, []byte, bool) {
	view := parseRawJSONView(body)
	if !view.IsObject() {
		return gjson.Result{}, nil, false
	}
	var value gjson.Result
	count := 0
	view.ForEach(func(key, candidate gjson.Result) bool {
		if key.String() == field {
			value = candidate
			count++
		}
		return count < 2
	})
	if count == 0 {
		return value, nil, true
	}
	if count != 1 || value.Index < 1 || value.Index+len(value.Raw) > len(body) {
		return value, nil, false
	}
	return value, body[value.Index : value.Index+len(value.Raw)], true
}

func promptRulesUndo(input, output []byte, application BusinessSystemPromptApplication) []promptRulesCarrierUndo {
	proofs := []promptRulesCarrierUndo{}
	if !application.Applied || application.RulesPlan == nil {
		return proofs
	}
	for _, field := range []string{"instructions", "input", "messages"} {
		placements := []extensionv1.PromptRulePlacement{}
		for _, placement := range application.RulesPlan.Placements {
			if placement.Carrier == field {
				placements = append(placements, placement)
			}
		}
		if len(placements) == 0 {
			continue
		}
		before, beforeRaw, uniqueBefore := uniquePromptField(input, field)
		after, afterRaw, uniqueAfter := uniquePromptField(output, field)
		proof := promptRulesCarrierUndo{field: field, beforeExists: before.Exists(), beforeHash: promptRuleFieldHash(beforeRaw), afterHash: promptRuleFieldHash(afterRaw), valid: uniqueBefore && uniqueAfter, restorable: true}
		if field == "instructions" || !before.IsArray() {
			if len(beforeRaw) > businessSystemPromptRestoreMaxBytes {
				proof.restorable = false
			} else {
				proof.scalar = append([]byte(nil), beforeRaw...)
			}
		} else {
			original := before.Array()
			controlEnd := 0
			for controlEnd < len(original) {
				role := original[controlEnd].Get("role").String()
				if role != "system" && role != "developer" {
					break
				}
				controlEnd++
			}
			counts := map[int]int{}
			for _, placement := range placements {
				index := 0
				if placement.Position == extensionv1.PromptPositionControlAppend {
					index = controlEnd
				} else if placement.Position == extensionv1.PromptPositionConversationTail {
					index = len(original)
				}
				counts[index]++
			}
			added := 0
			for index := 0; index <= len(original); index++ {
				for n := 0; n < counts[index]; n++ {
					proof.indices = append(proof.indices, index+added)
					added++
				}
			}
			proof.valid = proof.valid && after.IsArray()
		}
		proofs = append(proofs, proof)
	}
	return proofs
}

func restorePromptRules(body []byte, proofs []promptRulesCarrierUndo) ([]byte, error) {
	out := body
	for _, proof := range proofs {
		value, raw, unique := uniquePromptField(out, proof.field)
		if !unique || !proof.valid {
			return nil, ErrBusinessSystemPromptUnavailable
		}
		if !value.Exists() {
			continue
		}
		hash := promptRuleFieldHash(raw)
		if value.Exists() == proof.beforeExists && hash == proof.beforeHash {
			continue
		}
		if hash != proof.afterHash || !proof.restorable {
			return nil, ErrBusinessSystemPromptUnavailable
		}
		var err error
		if !proof.beforeExists {
			out, err = sjson.DeleteBytes(out, proof.field)
		} else if proof.scalar != nil {
			out, err = sjson.SetRawBytes(out, proof.field, proof.scalar)
		} else {
			indices := append([]int(nil), proof.indices...)
			sort.Sort(sort.Reverse(sort.IntSlice(indices)))
			for _, index := range indices {
				out, err = sjson.DeleteBytes(out, fmt.Sprintf("%s.%d", proof.field, index))
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func sameBusinessPromptPlan(left, right BusinessSystemPromptApplication) bool {
	left.ClientInstructions, right.ClientInstructions = "", ""
	left.FinalInstructions, right.FinalInstructions = "", ""
	left.PublicInstructions, right.PublicInstructions = "", ""
	left.PublicInstructionsExists, right.PublicInstructionsExists = false, false
	left.OriginalInstructionsExists, right.OriginalInstructionsExists = false, false
	leftPlan, rightPlan := left.RulesPlan, right.RulesPlan
	left.RulesPlan, right.RulesPlan = nil, nil
	if left != right {
		return false
	}
	return (leftPlan == nil && rightPlan == nil) || (leftPlan != nil && rightPlan != nil && leftPlan.SHA256 == rightPlan.SHA256)
}

func validateBusinessSystemPromptFinal(c *gin.Context, body []byte, protocol string) error {
	value, exists := businessSystemPromptRequestGet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, protocol))
	if !exists {
		return nil
	}
	state, ok := value.(businessSystemPromptRequestState)
	if !ok || !state.application.Applied || state.application.RulesPlan == nil {
		return nil
	}
	for _, proof := range state.rulesUndo {
		if !promptRulesUseCarrier(state.application, proof.field) {
			continue
		}
		_, raw, unique := uniquePromptField(body, proof.field)
		if !unique || !proof.valid || promptRuleFieldHash(raw) != proof.afterHash {
			return fmt.Errorf("%w: final prompt carrier changed", ErrBusinessSystemPromptUnavailable)
		}
	}
	return nil
}

// Used by integrity checks to remove only this request's proven insertions.
func restorePromptRulesForIntegrity(c *gin.Context, body []byte) ([]byte, error) {
	value, exists := businessSystemPromptRequestGet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolResponses))
	if !exists {
		return body, nil
	}
	state, ok := value.(businessSystemPromptRequestState)
	if !ok || state.application.RulesPlan == nil {
		return body, nil
	}
	return restorePromptRules(body, state.rulesUndo)
}

func promptRulesCarrierMatches(body []byte, state businessSystemPromptRequestState) bool {
	if sha256.Sum256(body) == state.inputHash {
		return false
	}
	for _, proof := range state.rulesUndo {
		value, raw, unique := uniquePromptField(body, proof.field)
		if !unique {
			return false
		}
		if promptRulesUseCarrier(state.application, proof.field) {
			if promptRuleFieldHash(raw) != proof.afterHash {
				return false
			}
		} else if value.Exists() && promptRuleFieldHash(raw) != proof.beforeHash {
			return false
		}
	}
	return len(state.rulesUndo) > 0
}

func promptRulesUseCarrier(application BusinessSystemPromptApplication, field string) bool {
	if application.RulesPlan == nil {
		return false
	}
	for _, placement := range application.RulesPlan.Placements {
		if placement.Carrier == field {
			return true
		}
	}
	return false
}

func rewritePromptRulesResponse(body []byte, application BusinessSystemPromptApplication, expose bool) ([]byte, error) {
	if expose || application.PreserveInstructionsEcho || !application.Applied || application.FinalInstructions == "" || !json.Valid(body) {
		return body, nil
	}
	out := body
	for _, path := range []string{"instructions", "response.instructions", "error.instructions", "error.response.instructions"} {
		value := gjson.GetBytes(out, path)
		if value.Type != gjson.String || value.String() != application.FinalInstructions {
			continue
		}
		var err error
		if application.PublicInstructionsExists {
			out, err = sjson.SetBytes(out, path, application.PublicInstructions)
		} else {
			out, err = sjson.DeleteBytes(out, path)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Only redact exact structured echoes of carriers we changed. Never search
// model prose for matching text or remove a customer's identical message.
func rewritePromptRulesStructuredEcho(c *gin.Context, body []byte, protocol string) []byte {
	value, exists := businessSystemPromptRequestGet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, protocol))
	if !exists || !json.Valid(body) {
		return body
	}
	state, ok := value.(businessSystemPromptRequestState)
	if !ok || state.application.RulesPlan == nil || state.application.ExposeServerPrompt || state.application.PreserveInstructionsEcho {
		return body
	}
	out := body
	for _, proof := range state.rulesUndo {
		if proof.field == "instructions" {
			continue
		}
		for _, prefix := range []string{"", "response.", "error.", "error.response.", "error.body."} {
			path := prefix + proof.field
			echo := gjson.GetBytes(out, path)
			if !echo.Exists() || promptRuleFieldHash([]byte(echo.Raw)) != proof.afterHash {
				continue
			}
			envelope, err := sjson.SetRawBytes([]byte(`{}`), proof.field, []byte(echo.Raw))
			if err != nil {
				continue
			}
			clean, err := restorePromptRules(envelope, []promptRulesCarrierUndo{proof})
			if err != nil {
				continue
			}
			original := gjson.GetBytes(clean, proof.field)
			if original.Exists() {
				out, err = sjson.SetRawBytes(out, path, []byte(original.Raw))
			} else {
				out, err = sjson.DeleteBytes(out, path)
			}
			if err != nil {
				return body
			}
		}
	}
	return out
}

func rewritePromptRulesStructuredSSE(c *gin.Context, body []byte, protocol string) []byte {
	lines := bytes.SplitAfter(body, []byte("\n"))
	var result bytes.Buffer
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("data:")) {
			start := len("data:")
			for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
				start++
			}
			end := len(line)
			for end > start && (line[end-1] == '\n' || line[end-1] == '\r') {
				end--
			}
			result.Write(line[:start])
			result.Write(rewritePromptRulesStructuredEcho(c, line[start:end], protocol))
			result.Write(line[end:])
		} else {
			result.Write(line)
		}
	}
	return result.Bytes()
}

const promptRequestedModelContextKey = "business_prompt_requested_model"

func rememberPromptRequestedModel(c *gin.Context, body []byte) {
	if c == nil {
		return
	}
	if _, exists := businessSystemPromptRequestGet(c, promptRequestedModelContextKey); exists {
		return
	}
	if model := gjson.GetBytes(body, "model").String(); model != "" {
		businessSystemPromptRequestSet(c, promptRequestedModelContextKey, model)
	}
}

func enrichPromptTarget(c *gin.Context, body []byte, target BusinessSystemPromptTarget) BusinessSystemPromptTarget {
	target.UpstreamModel = gjson.GetBytes(body, "model").String()
	target.RequestedModel = target.UpstreamModel
	if value, exists := businessSystemPromptRequestGet(c, promptRequestedModelContextKey); exists {
		if model, ok := value.(string); ok && model != "" {
			target.RequestedModel = model
		}
	}
	return target
}

func (s *OpenAIGatewayService) finalizeBusinessPromptForSend(c *gin.Context, account *Account, body []byte, protocol string, compact bool) ([]byte, error) {
	updated, application, err := s.applyBusinessSystemPromptForRequest(c, body, account, protocol, compact)
	if err != nil {
		return nil, err
	}
	updated, err = rewriteBusinessSystemPromptCacheKey(c, updated, application)
	if err != nil {
		return nil, err
	}
	if err := validateBusinessSystemPromptFinal(c, updated, protocol); err != nil {
		return nil, err
	}
	observePromptRulesFinal(c, account, protocol, application)
	return updated, nil
}

func writePromptDeliveryError(c *gin.Context, err error) {
	if errors.Is(err, ErrPromptDeliveryUnsupported) && c != nil && c.Writer != nil && !c.Writer.Written() {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "prompt_delivery_unsupported", "message": "The configured prompt role or position is not supported by this destination"}})
	}
}
