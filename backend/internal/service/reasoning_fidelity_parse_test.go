//go:build reasoning_fidelity

package service_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
)

const fidelityMaxResponseBytes = 8 << 20

// These fixtures contain the problem and output contract, never the answer.
const fidelitySinglePrompt = `Arrange the six distinct letters A, B, C, D, E, F in a line, using each letter exactly once. A and B must both occur before D. B must occur before E. C and D must both occur before F. E must not occur immediately before F. Count every valid arrangement, and determine the lexicographically first and last valid arrangements using A < B < C < D < E < F. Return only one JSON object with exactly these fields: "count" (integer), "first" (six-letter string), "last" (six-letter string). Do not include prose or the reasoning process.`

const fidelityOrdersPrompt = `Use the read-only load_orders tool exactly once to obtain the available orders before solving this task. Select at most three distinct orders, with total hours no greater than 11. Orders A and D are mutually exclusive. Order E may be selected only if order B is also selected. Maximize total value; break ties by minimizing total hours, then by the lexicographic order of the selected IDs sorted ascending. Remember these constraints when the tool result arrives; do not ask the tool for an answer or further data. After receiving its result, return only one JSON object with exactly these fields: "selected" (array of order IDs sorted ascending), "hours" (integer), "value" (integer). Do not include prose or the reasoning process.`

const fidelityOrdersResult = `{"orders":[{"id":"A","hours":4,"value":13},{"id":"B","hours":3,"value":8},{"id":"C","hours":5,"value":16},{"id":"D","hours":6,"value":20},{"id":"E","hours":4,"value":15},{"id":"F","hours":2,"value":6}]}`

type fidelityUsage struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	ReasoningTokens *int64 `json:"reasoning_tokens"`
	CachedTokens    *int64 `json:"cached_tokens"`
	TotalTokens     *int64 `json:"total_tokens"`
}

type fidelityResponse struct {
	Status            string
	Model             string
	ID                string
	Output            []json.RawMessage
	Usage             *fidelityUsage
	Text              string
	HasError          bool
	HasIncomplete     bool
	HasRefusal        bool
	HasTool           bool
	ReasoningComplete bool
	RawReasoning      []json.RawMessage
	ErrorClass        string
}

// fidelityParseResponse is deliberately not a forgiving client renderer. It
// consumes one bounded response, requires explicit terminal evidence, and never
// makes a partial response look completed. Raw output is used for replay, not a
// narrower decoded/re-encoded representation that could discard unknown fields.
func fidelityParseResponse(body []byte, contentType string, httpStatus int) fidelityResponse {
	result := fidelityResponse{Status: "unknown"}
	if len(body) > fidelityMaxResponseBytes {
		result.HasError, result.ErrorClass, result.Status = true, "body_limit", "failed"
		return result
	}
	trimmed := bytes.TrimSpace(body)
	terminal := false
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") ||
		bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) ||
		bytes.HasPrefix(trimmed, []byte(":")) {
		result, terminal = fidelityParseSSE(body)
	} else {
		parsed, _, err := fidelityParseDocument(trimmed)
		if err != nil {
			result.HasError, result.ErrorClass = true, "parse"
		} else {
			result = parsed
			terminal = result.Status == "completed" || result.Status == "failed" || result.Status == "incomplete" || result.Status == "cancelled"
		}
	}

	// Status codes are transport evidence, not claims about a provider's balance.
	// In particular, a generic 429 is a rate limit, not proof of exhausted credit.
	if httpStatus < 200 || httpStatus >= 300 {
		result.HasError = true
		switch httpStatus {
		case 401, 403:
			result.ErrorClass = "auth"
		case 402:
			if result.ErrorClass != "quota" {
				result.ErrorClass = "payment_required"
			}
		case 429:
			if result.ErrorClass != "quota" && result.ErrorClass != "auth" {
				result.ErrorClass = "rate_limit"
			}
		default:
			if result.ErrorClass == "" {
				result.ErrorClass = "http"
			}
		}
	}
	if !terminal && !result.HasError {
		result.HasIncomplete, result.ErrorClass = true, "eof"
	}
	fidelityInspectOutput(&result)
	if result.HasError {
		result.Status = "failed"
		result.ReasoningComplete = false
	} else if result.HasIncomplete || !terminal {
		result.Status = "incomplete"
		result.ReasoningComplete = false
	}
	return result
}

func fidelityParseDocument(data []byte) (fidelityResponse, bool, error) {
	result := fidelityResponse{Status: "unknown"}
	object, err := fidelityJSONObject(data)
	if err != nil {
		return result, false, err
	}
	for name, target := range map[string]*string{"id": &result.ID, "model": &result.Model, "status": &result.Status} {
		if raw, ok := object[name]; ok {
			if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, target) != nil {
				return result, false, errors.New("invalid response scalar")
			}
		}
	}
	output, outputPresent := object["output"]
	if outputPresent {
		if bytes.Equal(bytes.TrimSpace(output), []byte("null")) || json.Unmarshal(output, &result.Output) != nil {
			return result, false, errors.New("invalid response output")
		}
	}
	if raw := object["usage"]; fidelityNonNull(raw) {
		result.Usage, err = fidelityParseUsage(raw)
		if err != nil {
			return result, outputPresent, err
		}
	}
	if raw := object["error"]; fidelityNonNull(raw) {
		result.HasError = true
		result.ErrorClass = fidelityStructuredErrorClass(raw)
	}
	// Some providers return a bare structured error instead of {error:{...}}.
	if raw := object["type"]; len(raw) != 0 {
		var kind string
		if json.Unmarshal(raw, &kind) == nil && (kind == "error" || strings.HasSuffix(kind, "_error")) {
			result.HasError = true
			result.ErrorClass = fidelityStructuredErrorClass(data)
		}
	}
	if fidelityNonNull(object["incomplete_details"]) || result.Status == "incomplete" || result.Status == "cancelled" {
		result.HasIncomplete = true
	}
	if result.Status == "failed" {
		result.HasError = true
		if result.ErrorClass == "" {
			result.ErrorClass = "upstream"
		}
	}
	return result, outputPresent, nil
}

func fidelityParseUsage(raw json.RawMessage) (*fidelityUsage, error) {
	object, err := fidelityJSONObject(raw)
	if err != nil {
		return nil, err
	}
	usage := &fidelityUsage{}
	for name, target := range map[string]**int64{
		"input_tokens": &usage.InputTokens, "output_tokens": &usage.OutputTokens, "total_tokens": &usage.TotalTokens,
	} {
		if err := fidelityReadTokenCount(object[name], target); err != nil {
			return nil, err
		}
	}
	for name, field := range map[string]struct {
		key    string
		target **int64
	}{
		"input_tokens_details":  {"cached_tokens", &usage.CachedTokens},
		"output_tokens_details": {"reasoning_tokens", &usage.ReasoningTokens},
	} {
		if !fidelityNonNull(object[name]) {
			continue
		}
		details, err := fidelityJSONObject(object[name])
		if err != nil {
			return nil, err
		}
		if err := fidelityReadTokenCount(details[field.key], field.target); err != nil {
			return nil, err
		}
	}
	return usage, nil
}

func fidelityReadTokenCount(raw json.RawMessage, target **int64) error {
	if !fidelityNonNull(raw) {
		return nil
	}
	var count int64
	if json.Unmarshal(raw, &count) != nil || count < 0 {
		return errors.New("invalid token count")
	}
	*target = &count
	return nil
}

func fidelityParseSSE(body []byte) (fidelityResponse, bool) {
	result := fidelityResponse{Status: "unknown"}
	items := make(map[int]json.RawMessage)
	announced := make(map[int]json.RawMessage)
	terminal, finalOutputPresent, doneMarker := false, false, false
	eventName := ""
	var data []byte
	hadData := false
	setError := func(class string) {
		result.HasError = true
		if result.ErrorClass == "" || class == "auth" || class == "quota" {
			result.ErrorClass = class
		}
	}
	apply := func() {
		if !hadData {
			return
		}
		payload := bytes.TrimSuffix(data, []byte{'\n'})
		if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
			doneMarker = true
			return
		}
		if doneMarker {
			setError("parse")
			return
		}
		object, err := fidelityJSONObject(payload)
		if err != nil {
			setError("parse")
			return
		}
		var kind string
		if raw, ok := object["type"]; ok {
			if json.Unmarshal(raw, &kind) != nil {
				setError("parse")
				return
			}
		}
		if kind == "" {
			kind = eventName
		} else if eventName != "" && eventName != "message" && eventName != kind {
			setError("parse")
			return
		}
		if kind == "error" || kind == "response.error" {
			errRaw := object["error"]
			if !fidelityNonNull(errRaw) {
				errRaw = payload
			}
			setError(fidelityStructuredErrorClass(errRaw))
			return
		}
		if fidelityNonNull(object["error"]) {
			setError(fidelityStructuredErrorClass(object["error"]))
		}
		if terminal {
			// No response-mutating event may add content after its terminal
			// document. Only SSE comments and the [DONE] trailer are allowed.
			setError("parse")
			return
		}
		if kind == "response.refusal.delta" || kind == "response.refusal.done" {
			result.HasRefusal = true
		}
		if kind == "response.output_item.done" || kind == "response.output_item.added" {
			var index int
			if !fidelityNonNull(object["output_index"]) || json.Unmarshal(object["output_index"], &index) != nil || index < 0 || index > fidelityMaxResponseBytes {
				setError("parse")
				return
			}
			item := object["item"]
			if _, err := fidelityJSONObject(item); err != nil {
				setError("parse")
				return
			}
			observed := fidelityResponse{Status: "completed", Output: []json.RawMessage{item}}
			fidelityInspectOutput(&observed)
			result.HasTool = result.HasTool || observed.HasTool
			result.HasRefusal = result.HasRefusal || observed.HasRefusal
			if observed.HasError {
				setError(observed.ErrorClass)
			}
			if kind == "response.output_item.added" {
				if previous, exists := announced[index]; exists && !fidelitySameItemIdentity(previous, item) {
					setError("parse")
					return
				}
				if previous, exists := items[index]; exists && !fidelitySameItemIdentity(previous, item) {
					setError("parse")
					return
				}
				announced[index] = item
				return
			}
			result.HasIncomplete = result.HasIncomplete || observed.HasIncomplete
			if previous, exists := announced[index]; exists && !fidelitySameItemIdentity(previous, item) {
				setError("parse")
				return
			}
			if previous, exists := items[index]; exists && !bytes.Equal(previous, item) {
				setError("parse")
				return
			}
			items[index] = item
			return
		}
		if kind != "response.completed" && kind != "response.done" && kind != "response.failed" && kind != "response.incomplete" {
			return
		}
		parsed, outputPresent, err := fidelityParseDocument(object["response"])
		if err != nil {
			setError("parse")
			return
		}
		if kind == "response.completed" && parsed.Status != "completed" ||
			kind == "response.failed" && parsed.Status != "failed" ||
			kind == "response.incomplete" && parsed.Status != "incomplete" {
			setError("parse")
			return
		}
		if parsed.Status != "completed" && parsed.Status != "failed" && parsed.Status != "incomplete" && parsed.Status != "cancelled" {
			setError("parse")
			return
		}
		terminal = true
		result.Status, result.Model, result.ID = parsed.Status, parsed.Model, parsed.ID
		result.Output, result.Usage, finalOutputPresent = parsed.Output, parsed.Usage, outputPresent
		result.HasIncomplete = result.HasIncomplete || parsed.HasIncomplete
		if parsed.HasError {
			setError(parsed.ErrorClass)
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), fidelityMaxResponseBytes+1)
	scanner.Split(fidelitySSELine)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			apply()
			data, eventName, hadData = data[:0], "", false
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte{' '})
		switch string(field) {
		case "event":
			eventName = string(value)
		case "data":
			data = append(data, value...)
			data = append(data, '\n')
			hadData = true
		}
	}
	if scanner.Err() != nil {
		setError("parse")
	}
	if hadData {
		// A pending event at EOF was never terminated by its SSE blank line.
		result.HasIncomplete = true
		if result.ErrorClass == "" {
			result.ErrorClass = "eof"
		}
	}
	for index, item := range announced {
		var finished json.RawMessage
		if finalOutputPresent {
			if index < len(result.Output) {
				finished = result.Output[index]
			}
		} else {
			finished = items[index]
		}
		if len(finished) == 0 {
			result.HasIncomplete = true
			if result.ErrorClass == "" {
				result.ErrorClass = "eof"
			}
		} else if !fidelitySameItemIdentity(item, finished) {
			setError("parse")
		}
	}
	if !finalOutputPresent && len(items) > 0 {
		indexes := make([]int, 0, len(items))
		for index := range items {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for expected, index := range indexes {
			if expected != index {
				setError("parse")
				result.Output = nil
				break
			}
			result.Output = append(result.Output, items[index])
		}
	}
	return result, terminal
}

// Type and an already-issued ID cannot change at the same output index. The
// terminal representation may otherwise legitimately enrich an added item.
func fidelitySameItemIdentity(before, after json.RawMessage) bool {
	left, err := fidelityJSONObject(before)
	if err != nil {
		return false
	}
	right, err := fidelityJSONObject(after)
	if err != nil {
		return false
	}
	for _, key := range []string{"type", "id", "call_id"} {
		var expected, actual string
		if fidelityNonNull(left[key]) && json.Unmarshal(left[key], &expected) != nil {
			return false
		}
		if expected == "" {
			continue
		}
		if json.Unmarshal(right[key], &actual) != nil || expected != actual {
			return false
		}
	}
	return true
}

// The SSE line grammar permits LF, CRLF, and CR. Keep event boundaries rather
// than guessing that EOF means the current event was successfully completed.
func fidelitySSELine(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i], nil
		}
		if b == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			advance := i + 1
			if i+1 < len(data) && data[i+1] == '\n' {
				advance++
			}
			return advance, data[:i], nil
		}
	}
	if atEOF && len(data) != 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func fidelityInspectOutput(result *fidelityResponse) {
	var finalText, unphasedText strings.Builder
	hasFinalPhase, hasReasoning, allReasoningComplete := false, false, true
	setParseError := func() {
		result.HasError = true
		if result.ErrorClass != "auth" && result.ErrorClass != "quota" && result.ErrorClass != "rate_limit" {
			result.ErrorClass = "parse"
		}
	}
	for _, raw := range result.Output {
		object, err := fidelityJSONObject(raw)
		if err != nil {
			setParseError()
			continue
		}
		var kind, status string
		if json.Unmarshal(object["type"], &kind) != nil || kind == "" {
			setParseError()
			continue
		}
		if rawStatus := object["status"]; fidelityNonNull(rawStatus) {
			if json.Unmarshal(rawStatus, &status) != nil {
				setParseError()
				continue
			}
			if status == "incomplete" || status == "in_progress" || status == "failed" || status == "cancelled" {
				result.HasIncomplete = true
			}
		}
		if kind == "reasoning" {
			hasReasoning = true
			result.RawReasoning = append(result.RawReasoning, raw)
			var encrypted string
			if json.Unmarshal(object["encrypted_content"], &encrypted) != nil || encrypted == "" || (status != "" && status != "completed") {
				allReasoningComplete = false
			}
			continue
		}
		if strings.HasSuffix(kind, "_call") || strings.HasSuffix(kind, "_call_output") ||
			kind == "mcp_approval_request" || kind == "mcp_list_tools" {
			result.HasTool = true
		}
		if kind == "refusal" {
			result.HasRefusal = true
		}
		if kind != "message" {
			continue
		}
		var message struct {
			Role    string            `json:"role"`
			Phase   string            `json:"phase"`
			Content []json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &message) != nil {
			setParseError()
			continue
		}
		if message.Role == "assistant" && message.Phase == "final_answer" {
			hasFinalPhase = true
		}
		for _, rawPart := range message.Content {
			part, err := fidelityJSONObject(rawPart)
			var partType, partText string
			if err != nil || json.Unmarshal(part["type"], &partType) != nil {
				setParseError()
				continue
			}
			if partType == "refusal" {
				result.HasRefusal = true
			}
			if message.Role != "assistant" || partType != "output_text" {
				continue
			}
			if !fidelityNonNull(part["text"]) || json.Unmarshal(part["text"], &partText) != nil {
				setParseError()
				continue
			}
			switch message.Phase {
			case "final_answer":
				finalText.WriteString(partText)
			case "":
				unphasedText.WriteString(partText)
			}
		}
	}
	if hasFinalPhase {
		result.Text = finalText.String()
	} else {
		result.Text = unphasedText.String()
	}
	result.ReasoningComplete = result.Status == "completed" && !result.HasError && !result.HasIncomplete && hasReasoning && allReasoningComplete
}

func fidelityStructuredErrorClass(raw []byte) string {
	object, err := fidelityJSONObject(raw)
	if err != nil {
		return "upstream"
	}
	classification := "upstream"
	for _, key := range []string{"code", "type"} {
		var code string
		// Numeric codes and message prose are deliberately not classifications.
		if json.Unmarshal(object[key], &code) != nil {
			continue
		}
		switch code {
		case "authentication_error", "invalid_api_key", "unauthorized", "permission_denied", "access_denied", "forbidden", "invalid_authentication", "account_deactivated":
			return "auth"
		case "insufficient_quota", "quota_exceeded", "budget_exceeded", "billing_hard_limit_reached", "billing_not_active", "usage_limit_reached":
			classification = "quota"
		case "rate_limit_exceeded", "rate_limit_error", "rate_limit_reached", "too_many_requests":
			if classification != "quota" {
				classification = "rate_limit"
			}
		}
	}
	return classification
}

func fidelityNonNull(raw []byte) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Reject duplicate object keys and trailing JSON values for fields whose
// semantics the harness consumes. Unknown values remain untouched RawMessages.
func fidelityJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("expected JSON object")
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid object key")
		}
		if _, duplicate := object[key]; duplicate {
			return nil, errors.New("duplicate object key")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		object[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return object, nil
}

func fidelityScore(fixture string, text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lineEnd := strings.IndexByte(text, '\n')
		if lineEnd == -1 {
			return false
		}
		language := strings.TrimSpace(text[3:lineEnd])
		if language != "" && language != "json" {
			return false
		}
		rest := strings.TrimSpace(text[lineEnd+1:])
		lastLine := strings.LastIndexByte(rest, '\n')
		if lastLine == -1 || strings.TrimSpace(rest[lastLine+1:]) != "```" {
			return false
		}
		text = strings.TrimSpace(rest[:lastLine])
	}
	object, err := fidelityJSONObject([]byte(text))
	if err != nil || len(object) != 3 {
		return false
	}
	switch fixture {
	case "single", "pilot":
		var count int
		var first, last string
		return json.Unmarshal(object["count"], &count) == nil && count == 25 &&
			json.Unmarshal(object["first"], &first) == nil && first == "ABCDFE" &&
			json.Unmarshal(object["last"], &last) == nil && last == "CBEADF"
	case "tool", "tool_final":
		var selected []string
		var hours, value int
		return json.Unmarshal(object["selected"], &selected) == nil && len(selected) == 3 &&
			selected[0] == "A" && selected[1] == "B" && selected[2] == "E" &&
			json.Unmarshal(object["hours"], &hours) == nil && hours == 11 &&
			json.Unmarshal(object["value"], &value) == nil && value == 36
	default:
		return false
	}
}

func TestReasoningFidelityFixtures(t *testing.T) {
	t.Run("independent permutation truth", func(t *testing.T) {
		letters := []byte("ABCDEF")
		total := 0
		var valid []string
		var enumerate func(int)
		enumerate = func(index int) {
			if index == len(letters) {
				total++
				positions := make(map[byte]int, len(letters))
				for position, letter := range letters {
					positions[letter] = position
				}
				if positions['A'] < positions['D'] && positions['B'] < positions['D'] &&
					positions['B'] < positions['E'] && positions['C'] < positions['F'] &&
					positions['D'] < positions['F'] && positions['E']+1 != positions['F'] {
					valid = append(valid, string(letters))
				}
				return
			}
			for i := index; i < len(letters); i++ {
				letters[index], letters[i] = letters[i], letters[index]
				enumerate(index + 1)
				letters[index], letters[i] = letters[i], letters[index]
			}
		}
		enumerate(0)
		sort.Strings(valid)
		if total != 720 || len(valid) != 25 || valid[0] != "ABCDFE" || valid[len(valid)-1] != "CBEADF" {
			t.Fatal("independent permutation enumeration disagrees with fixed truth")
		}
	})
	t.Run("independent order truth", func(t *testing.T) {
		var catalog struct {
			Orders []struct {
				ID    string `json:"id"`
				Hours int    `json:"hours"`
				Value int    `json:"value"`
			} `json:"orders"`
		}
		if err := json.Unmarshal([]byte(fidelityOrdersResult), &catalog); err != nil || len(catalog.Orders) != 6 {
			t.Fatal("invalid order fixture")
		}
		total, bestHours, bestValue, bestIDs := 0, 0, -1, ""
		for mask := 0; mask < 1<<len(catalog.Orders); mask++ {
			total++
			var ids []string
			hours, value := 0, 0
			for index, order := range catalog.Orders {
				if mask&(1<<index) != 0 {
					ids, hours, value = append(ids, order.ID), hours+order.Hours, value+order.Value
				}
			}
			sort.Strings(ids)
			joined := strings.Join(ids, "")
			if len(ids) > 3 || hours > 11 || strings.Contains(joined, "A") && strings.Contains(joined, "D") ||
				strings.Contains(joined, "E") && !strings.Contains(joined, "B") {
				continue
			}
			if value > bestValue || value == bestValue && (hours < bestHours || hours == bestHours && joined < bestIDs) {
				bestIDs, bestHours, bestValue = joined, hours, value
			}
		}
		if total != 64 || bestIDs != "ABE" || bestHours != 11 || bestValue != 36 {
			t.Fatal("independent subset enumeration disagrees with fixed truth")
		}
	})
	t.Run("strict scoring", func(t *testing.T) {
		goodSingle := `{"count":25,"first":"ABCDFE","last":"CBEADF"}`
		goodTool := `{"selected":["A","B","E"],"hours":11,"value":36}`
		for _, text := range []string{goodSingle, "```json\n" + goodSingle + "\n```", "```\n" + goodSingle + "\n```"} {
			if !fidelityScore("single", text) {
				t.Fatal("valid single score rejected")
			}
		}
		if !fidelityScore("tool", goodTool) || !fidelityScore("tool_final", goodTool) || !fidelityScore("pilot", goodSingle) {
			t.Fatal("valid tool score rejected")
		}
		for _, text := range []string{
			`25 ABCDFE CBEADF`, `{"count":25}`, goodSingle + `{}`, "answer: " + goodSingle,
			`{"count":24,"count":25,"first":"ABCDFE","last":"CBEADF"}`,
			`{"count":25,"first":"ABCDFE","last":"CBEADF","extra":true}`,
			`{"count":25.0,"first":"ABCDFE","last":"CBEADF"}`,
			"```python\n" + goodSingle + "\n```", "```json\n" + goodSingle,
		} {
			if fidelityScore("single", text) {
				t.Fatal("partial or ambiguous single score accepted")
			}
		}
		for _, text := range []string{`{"selected":["A","B","E"]}`, `{"selected":["E","B","A"],"hours":11,"value":36}`, `{"selected":["A","B","E"],"hours":11,"value":35}`} {
			if fidelityScore("tool", text) {
				t.Fatal("invalid tool score accepted")
			}
		}
	})
	t.Run("json raw output and usage", func(t *testing.T) {
		reasoning := `{ "type":"reasoning", "status":"completed", "encrypted_content":"opaque-test-only", "summary":[], "future":{"preserve":true} }`
		answer := `{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"{\"count\":25,\"first\":\"ABCDFE\",\"last\":\"CBEADF\"}"}]}`
		body := `{"id":"resp_test","model":"test-model","status":"completed","output":[` + reasoning + `,` + answer + `],"usage":{"input_tokens":7,"output_tokens":9,"total_tokens":16,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":5}}}`
		got := fidelityParseResponse([]byte(body), "application/json", 200)
		if got.Status != "completed" || got.HasError || got.HasIncomplete || !got.ReasoningComplete || got.ID != "resp_test" || got.Model != "test-model" || !fidelityScore("single", got.Text) {
			t.Fatal("completed JSON response lost meaning")
		}
		if len(got.Output) != 2 || string(got.Output[0]) != reasoning || len(got.RawReasoning) != 1 || string(got.RawReasoning[0]) != reasoning {
			t.Fatal("raw output was changed")
		}
		if got.Usage == nil || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 7 || got.Usage.OutputTokens == nil || *got.Usage.OutputTokens != 9 ||
			got.Usage.CachedTokens == nil || *got.Usage.CachedTokens != 0 || got.Usage.ReasoningTokens == nil || *got.Usage.ReasoningTokens != 5 || got.Usage.TotalTokens == nil || *got.Usage.TotalTokens != 16 {
			t.Fatal("usage fields not preserved")
		}
	})
	t.Run("usage unknown is not zero", func(t *testing.T) {
		for _, suffix := range []string{"", `,"usage":null`} {
			got := fidelityParseResponse([]byte(`{"status":"completed","output":[]`+suffix+`}`), "application/json", 200)
			if got.Usage != nil {
				t.Fatal("absent usage synthesized")
			}
		}
		got := fidelityParseResponse([]byte(`{"status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens_details":{},"input_tokens_details":null}}`), "application/json", 200)
		if got.Usage == nil || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 0 || got.Usage.OutputTokens != nil || got.Usage.ReasoningTokens != nil || got.Usage.CachedTokens != nil || got.Usage.TotalTokens != nil {
			t.Fatal("unknown usage confused with measured zero")
		}
	})
	t.Run("sse final raw or ordered fallback", func(t *testing.T) {
		first := `{"type":"reasoning","encrypted_content":"opaque-test-only","summary":[]}`
		second := `{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"answer"}],"future":7}`
		prefix := "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":" + second + "}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + first + "}\n\n"
		for _, newline := range []string{"\n", "\r\n", "\r"} {
			body := prefix + "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"status\":\"completed\",\"id\":\"resp_test\"}}\n\ndata: [DONE]\n\n"
			got := fidelityParseResponse([]byte(strings.ReplaceAll(body, "\n", newline)), "text/event-stream", 200)
			if got.Status != "completed" || got.HasError || got.HasIncomplete || got.Text != "answer" || !got.ReasoningComplete || len(got.Output) != 2 || string(got.Output[0]) != first || string(got.Output[1]) != second {
				t.Fatal("SSE indexed fallback lost raw ordered output")
			}
		}
		body := prefix + "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"
		got := fidelityParseResponse([]byte(body), "text/event-stream", 200)
		if got.Status != "completed" || len(got.Output) != 0 || got.ReasoningComplete {
			t.Fatal("explicit final output was overridden by older events")
		}
	})
	t.Run("sse announced items must finish", func(t *testing.T) {
		added := "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_test\",\"status\":\"in_progress\",\"summary\":[]}}\n\n"
		done := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_test\",\"status\":\"completed\",\"encrypted_content\":\"opaque-test-only\",\"summary\":[]}}\n\n"
		terminal := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
		got := fidelityParseResponse([]byte(added+done+terminal), "text/event-stream", 200)
		if got.Status != "completed" || got.HasIncomplete || got.HasError || !got.ReasoningComplete {
			t.Fatal("completed announced item was not accepted")
		}
		unfinishedTool := "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"status\":\"in_progress\",\"call_id\":\"call_test\",\"name\":\"load_orders\",\"arguments\":\"\"}}\n\n"
		got = fidelityParseResponse([]byte(added+done+unfinishedTool+terminal), "text/event-stream", 200)
		if got.Status == "completed" || !got.HasIncomplete || !got.HasTool || got.ReasoningComplete {
			t.Fatal("unfinished announced tool silently omitted")
		}
		for _, body := range []string{terminal + done, added + terminal, added + strings.ReplaceAll(done, "rs_test", "rs_other") + terminal} {
			got := fidelityParseResponse([]byte(body), "text/event-stream", 200)
			if got.Status == "completed" || got.ReasoningComplete {
				t.Fatal("post-terminal or mismatched fallback item accepted")
			}
		}
		refusal := "data: {\"type\":\"response.refusal.delta\",\"delta\":\"test-only\"}\n\n"
		got = fidelityParseResponse([]byte(refusal+terminal), "text/event-stream", 200)
		if !got.HasRefusal {
			t.Fatal("observed refusal lost at terminal")
		}
	})
	t.Run("strict terminal evidence", func(t *testing.T) {
		completed := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"
		cases := []struct {
			body        string
			contentType string
		}{
			{"", "application/json"}, {`{"status":"in_progress","output":[]}`, "application/json"},
			{`{"status":"completed","output":[],"error":{"code":"test_error"}}`, "application/json"},
			{`{"status":"completed","output":[],"incomplete_details":{"reason":"max_output_tokens"}}`, "application/json"},
			{`{"status":"failed","output":[]}`, "application/json"}, {`{"status":"incomplete","output":[]}`, "application/json"},
			{`{"status":"completed","output":[]} {}`, "application/json"},
			{`{"status":"failed","status":"completed","output":[]}`, "application/json"},
			{"data: [DONE]\n\n", "text/event-stream"},
			{strings.TrimRight(completed, "\n"), "text/event-stream"},
			{"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"in_progress\"}}\n\n", "text/event-stream"},
			{"data: {\"type\":\"error\",\"code\":\"server_error\"}\n\n" + completed, "text/event-stream"},
			{completed + "data: {\"type\":\"error\",\"code\":\"server_error\"}\n\n", "text/event-stream"},
			{completed + completed, "text/event-stream"},
			{"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"reasoning\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "text/event-stream"},
			{completed + "data: {", "text/event-stream"},
		}
		for index, tc := range cases {
			got := fidelityParseResponse([]byte(tc.body), tc.contentType, 200)
			if got.Status == "completed" || got.ReasoningComplete || !got.HasError && !got.HasIncomplete {
				t.Fatalf("invalid response %d was treated as completed", index)
			}
		}
	})
	t.Run("structured errors only", func(t *testing.T) {
		cases := []struct {
			status int
			body   string
			class  string
		}{
			{401, `{"error":{"message":"do not record"}}`, "auth"},
			{403, `not JSON`, "auth"}, {402, `not JSON`, "payment_required"},
			{429, `{"error":{"message":"insufficient_quota budget_exceeded"}}`, "rate_limit"},
			{429, `{"error":{"type":"budget_exceeded","code":"429"}}`, "quota"},
			{200, `{"error":{"code":"insufficient_quota"}}`, "quota"},
			{200, `{"error":{"code":429,"message":"quota_exceeded"}}`, "upstream"},
			{200, `{"error":{"code":"invalid_api_key"}}`, "auth"},
			{401, `{"status":"completed","output":[null]}`, "auth"},
			{200, `{"type":"error","code":"rate_limit_exceeded"}`, "rate_limit"},
			{200, `{"error":{"code":"server_error","message":"invalid_api_key"}}`, "upstream"},
		}
		for index, tc := range cases {
			got := fidelityParseResponse([]byte(tc.body), "application/json", tc.status)
			if !got.HasError || got.Status == "completed" || got.ErrorClass != tc.class {
				t.Fatalf("error classification mismatch in case %d", index)
			}
		}
	})
	t.Run("phase refusal tool and incomplete reasoning", func(t *testing.T) {
		message := func(phase, text string) string {
			encoded, _ := json.Marshal(text)
			return fmt.Sprintf(`{"type":"message","role":"assistant","phase":%q,"content":[{"type":"output_text","text":%s}]}`, phase, encoded)
		}
		answer := `{"count":25,"first":"ABCDFE","last":"CBEADF"}`
		output := message("commentary", "not the answer") + "," + message("", "old answer") + "," + message("final_answer", answer)
		got := fidelityParseResponse([]byte(`{"status":"completed","output":[`+output+`]}`), "application/json", 200)
		if !fidelityScore("single", got.Text) || len(got.Output) != 3 {
			t.Fatal("phase selection polluted scoring or dropped replay output")
		}
		output = message("commentary", "not the answer") + "," + message("", answer)
		got = fidelityParseResponse([]byte(`{"status":"completed","output":[`+output+`]}`), "application/json", 200)
		if !fidelityScore("single", got.Text) {
			t.Fatal("commentary polluted unphased final answer")
		}
		got = fidelityParseResponse([]byte(`{"status":"completed","output":[{"type":"reasoning","status":"in_progress","encrypted_content":"test-only"},{"type":"function_call","call_id":"call_test","name":"load_orders","arguments":"{}"},{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"test-only"}]}]}`), "application/json", 200)
		if got.Status == "completed" || got.ReasoningComplete || !got.HasIncomplete || !got.HasTool || !got.HasRefusal {
			t.Fatal("unsafe continuation eligibility")
		}
		for _, part := range []string{
			`{"type":"refusal","type":"output_text","text":"{\"count\":25,\"first\":\"ABCDFE\",\"last\":\"CBEADF\"}"}`,
			`{"type":"output_text","text":"wrong","text":"{\"count\":25,\"first\":\"ABCDFE\",\"last\":\"CBEADF\"}"}`,
		} {
			got := fidelityParseResponse([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[`+part+`]}]}`), "application/json", 200)
			if !got.HasError || got.Status == "completed" || fidelityScore("single", got.Text) {
				t.Fatal("ambiguous content object accepted")
			}
		}
	})
	t.Run("bounded input and invalid usage", func(t *testing.T) {
		got := fidelityParseResponse(bytes.Repeat([]byte{' '}, fidelityMaxResponseBytes+1), "application/json", 200)
		if !got.HasError || got.ErrorClass != "body_limit" || got.Status == "completed" {
			t.Fatal("response body limit ignored")
		}
		for _, value := range []string{`-1`, `1.2`, `"9"`, `9223372036854775808`} {
			got := fidelityParseResponse([]byte(`{"status":"completed","output":[],"usage":{"input_tokens":`+value+`}}`), "application/json", 200)
			if !got.HasError || got.Status == "completed" {
				t.Fatal("invalid usage accepted")
			}
		}
	})
}
