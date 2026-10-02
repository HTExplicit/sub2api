package service

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// codexLogicalTurnContextKey holds the logical turn staged for the current
// request (empty when the request names none).
const codexLogicalTurnContextKey = "codex_logical_turn_id"

// A turn identifier is independent of the conversation/session. Opaque state
// cannot cross a turn simply because the same downstream session continues.
func stageCodexLogicalTurn(c *gin.Context, body []byte) {
	stageCodexLogicalTurnWithHeader(c, body, true)
}

// A persistent WS handshake header describes the initial request only. Each
// frame needs its own explicit turn identity before opaque state may be reused.
func stageCodexLogicalWSTurn(c *gin.Context, body []byte) {
	stageCodexLogicalTurnWithHeader(c, body, false)
}

func stageCodexLogicalTurnWithHeader(c *gin.Context, body []byte, allowHeader bool) {
	if c == nil {
		return
	}
	turn := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.turn_id").String())
	if turn == "" {
		metadata := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()
		turn = strings.TrimSpace(gjson.Get(metadata, "turn_id").String())
	}
	if turn == "" && allowHeader && c.Request != nil {
		turn = strings.TrimSpace(gjson.Get(c.Request.Header.Get(openAIWSTurnMetadataHeader), "turn_id").String())
	}
	if len(turn) > 128 {
		turn = ""
	}
	c.Set(codexLogicalTurnContextKey, turn)
}

func codexLogicalTurnID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, exists := c.Get(codexLogicalTurnContextKey); exists {
		text, _ := value.(string)
		return text
	}
	if c.Request == nil {
		return ""
	}
	turn := strings.TrimSpace(gjson.Get(c.Request.Header.Get(openAIWSTurnMetadataHeader), "turn_id").String())
	if len(turn) > 128 {
		return ""
	}
	return turn
}
