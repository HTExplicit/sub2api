package service

import (
	"bytes"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// finalizeResponsesForSend applies the configured system prompt at the final
// Responses send boundary.
func (s *OpenAIGatewayService) finalizeResponsesForSend(c *gin.Context, account *Account, body []byte) []byte {
	if s != nil {
		body = s.systemPrompts.applyResponses(c, account, body)
	}
	return body
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
