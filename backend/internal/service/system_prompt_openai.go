package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// finalizeResponsesForSend builds the body for one Responses send from the
// caller's clean body: the system prompt first, then Cindy's final-wire
// prompt_cache_key normalization. The clean body stays the retry source.
func (s *OpenAIGatewayService) finalizeResponsesForSend(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	if s != nil {
		body = s.systemPrompts.applyResponses(c, account, body)
	}
	updated, changed, err := normalizeCindyManagedPromptCacheKey(body, c, account)
	if err != nil {
		return nil, fmt.Errorf("normalize final Cindy prompt_cache_key: %w", err)
	}
	if changed {
		observeCindyManagedPromptCacheNormalization(c, true)
	}
	return updated, nil
}

var systemPromptEchoMarker = []byte(`"instructions"`)

// restoreSystemPromptEcho puts the client's own instructions back into an
// echoed Responses object (JSON body, SSE data or WS event).
func restoreSystemPromptEcho(c *gin.Context, body []byte) []byte {
	echo := systemPromptEchoFrom(c)
	if echo == nil || !echo.instructions || !bytes.Contains(body, systemPromptEchoMarker) {
		return body
	}
	out := body
	for _, path := range []string{"instructions", "response.instructions"} {
		value := gjson.GetBytes(out, path)
		if value.Type != gjson.String || value.String() != echo.finalInstructions {
			continue
		}
		var err error
		if echo.clientHadInstructs {
			out, err = sjson.SetBytes(out, path, echo.clientInstructions)
		} else {
			out, err = sjson.SetRawBytes(out, path, []byte("null"))
		}
		if err != nil {
			return body
		}
	}
	return out
}

// restoreSystemPromptEchoSSE applies restoreSystemPromptEcho to every data line.
func restoreSystemPromptEchoSSE(c *gin.Context, body []byte) []byte {
	echo := systemPromptEchoFrom(c)
	if echo == nil || !echo.instructions || !bytes.Contains(body, systemPromptEchoMarker) {
		return body
	}
	var result bytes.Buffer
	for _, line := range bytes.SplitAfter(body, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			_, _ = result.Write(line)
			continue
		}
		start := len("data:")
		for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
			start++
		}
		end := len(line)
		for end > start && (line[end-1] == '\n' || line[end-1] == '\r') {
			end--
		}
		_, _ = result.Write(line[:start])
		_, _ = result.Write(restoreSystemPromptEcho(c, line[start:end]))
		_, _ = result.Write(line[end:])
	}
	return result.Bytes()
}

// undoSystemPromptForIntegrity removes this send's injection before the
// request-integrity comparison with the client body.
func undoSystemPromptForIntegrity(c *gin.Context, body []byte) []byte {
	echo := systemPromptEchoFrom(c)
	if echo == nil {
		return body
	}
	if echo.instructions {
		value := gjson.GetBytes(body, "instructions")
		if value.Type != gjson.String || value.String() != echo.finalInstructions {
			return body
		}
		var err error
		out := body
		if echo.clientHadInstructs {
			out, err = sjson.SetBytes(body, "instructions", echo.clientInstructions)
		} else {
			out, err = sjson.DeleteBytes(body, "instructions")
		}
		if err != nil {
			return body
		}
		return out
	}
	if echo.inputIndex < 0 {
		return body
	}
	item := gjson.GetBytes(body, systemPromptInputPath(echo.inputIndex))
	if item.Get("role").String() != "developer" || item.Get("content.#").Int() != 1 || item.Get("content.0.text").String() != echo.developerPromptText {
		return body
	}
	out, err := sjson.DeleteBytes(body, systemPromptInputPath(echo.inputIndex))
	if err != nil {
		return body
	}
	if echo.inputWasString {
		rest := gjson.GetBytes(out, "input").Array()
		if len(rest) == 1 && rest[0].Get("role").String() == "user" && rest[0].Get("content").String() == echo.clientInputText {
			if restored, setErr := sjson.SetBytes(out, "input", echo.clientInputText); setErr == nil {
				return restored
			}
		}
	}
	return out
}

var systemPromptRetryInputIndex = regexp.MustCompile(`input\[(\d+)\]`)

// normalizeSystemPromptRejectedFieldRetryBody translates a provider's input
// indices from the sent array to the clean array before the bounded
// rejected-field repair runs. A rejection of the injected item is never
// repaired by editing a customer item at the same index.
func normalizeSystemPromptRejectedFieldRetryBody(c *gin.Context, status int, clean, response []byte) ([]byte, string, bool, error) {
	echo := systemPromptEchoFrom(c)
	if echo == nil || echo.inputIndex < 0 {
		return normalizeOpenAIResponsesRejectedFieldRetryBody(status, clean, response)
	}
	out := response
	for _, field := range []string{"error.param", "error.message", "param", "message"} {
		text := gjson.GetBytes(out, field)
		if text.Type != gjson.String {
			continue
		}
		owned := false
		mapped := systemPromptRetryInputIndex.ReplaceAllStringFunc(text.String(), func(match string) string {
			index, err := strconv.Atoi(systemPromptRetryInputIndex.FindStringSubmatch(match)[1])
			if err != nil || index == echo.inputIndex {
				owned = true
				return match
			}
			if index > echo.inputIndex {
				index--
			}
			return "input[" + strconv.Itoa(index) + "]"
		})
		if owned {
			return nil, "", false, nil
		}
		var err error
		out, err = sjson.SetBytes(out, field, mapped)
		if err != nil {
			return nil, "", false, err
		}
	}
	return normalizeOpenAIResponsesRejectedFieldRetryBody(status, clean, out)
}

var errReasoningWireProjection = errors.New("reasoning recovery edit cannot be applied to the clean request")

// prepareReasoningRecoveryRequest lets reasoning recovery inspect the exact
// wire body produced by the builder, then repeats its edits on the clean body
// that later retries rebuild from.
func prepareReasoningRecoveryRequest(recovery *openAIReasoningRecoveryState, req *http.Request, clean []byte, proxyURL string) (*http.Request, []byte, []byte, error) {
	wire, err := finalWireRequestBody(req, clean)
	if err != nil {
		return nil, nil, nil, err
	}
	prepared, final, err := recovery.PrepareRequest(req, wire, proxyURL)
	if err != nil {
		return nil, nil, nil, err
	}
	clean, err = projectReasoningCipherEdits(clean, wire, final)
	if err != nil {
		return nil, nil, nil, err
	}
	return prepared, clean, final, nil
}

// finalWireRequestBody reads the body a builder attached to req.
func finalWireRequestBody(req *http.Request, fallback []byte) ([]byte, error) {
	if req.GetBody != nil {
		reader, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		wire, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		return wire, nil
	}
	return fallback, nil
}

// finalWirePromptCacheKey prefers the key actually sent (Cindy may shorten it)
// and falls back to the caller's seed when the body carries none.
func finalWirePromptCacheKey(wire []byte, seed string) string {
	if value := gjson.GetBytes(wire, "prompt_cache_key"); value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
		return strings.TrimSpace(value.String())
	}
	return seed
}

// projectReasoningCipherEdits repeats a recovery edit on the clean request.
// Recovery may only delete encrypted_content from reasoning items, whose
// relative order the system prompt never changes; any other difference is
// rejected instead of becoming the next retry's source.
func projectReasoningCipherEdits(clean, before, after []byte) ([]byte, error) {
	if bytes.Equal(before, after) {
		return clean, nil
	}
	wireItems, cleanItems := openAIReasoningCipherItems(before), openAIReasoningCipherItems(clean)
	if len(wireItems) != len(cleanItems) {
		return nil, errReasoningWireProjection
	}
	wireIndices, cleanIndices := []int{}, []int{}
	for index, item := range wireItems {
		if item.hash != cleanItems[index].hash {
			return nil, errReasoningWireProjection
		}
		if !gjson.GetBytes(after, fmt.Sprintf("input.%d.encrypted_content", item.index)).Exists() {
			wireIndices = append(wireIndices, item.index)
			cleanIndices = append(cleanIndices, cleanItems[index].index)
		}
	}
	expected, err := stripOpenAIReasoningCipherIndices(before, wireIndices)
	if err != nil || !bytes.Equal(expected, after) {
		return nil, errReasoningWireProjection
	}
	return stripOpenAIReasoningCipherIndices(clean, cleanIndices)
}
