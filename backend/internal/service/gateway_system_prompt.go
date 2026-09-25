package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (s *GatewayService) SetBusinessSystemPromptService(prompts *BusinessSystemPromptService) {
	if s != nil {
		s.businessPromptService = prompts
	}
}

func (s *GeminiMessagesCompatService) SetBusinessSystemPromptService(prompts *BusinessSystemPromptService) {
	if s != nil {
		s.businessPromptService = prompts
	}
}

func (s *AntigravityGatewayService) SetBusinessSystemPromptService(prompts *BusinessSystemPromptService) {
	if s != nil {
		s.businessPromptService = prompts
	}
}

func rememberPromptRequestedModelName(c *gin.Context, model string) {
	if model == "" {
		return
	}
	if _, exists := businessSystemPromptRequestGet(c, promptRequestedModelContextKey); !exists {
		businessSystemPromptRequestSet(c, promptRequestedModelContextKey, model)
	}
}

// applyGeminiPromptForSend operates on the final Gemini request, including the
// Code Assist/Antigravity envelope. Provider metadata stays in the envelope;
// only request.systemInstruction belongs to the prompt engine.
func applyGeminiPromptForSend(prompts *BusinessSystemPromptService, c *gin.Context, account *Account, body []byte, model string) ([]byte, error) {
	for _, field := range []string{"request", "generateContentRequest"} {
		inner := gjson.GetBytes(body, field)
		if inner.IsObject() {
			if envelopeModel := gjson.GetBytes(body, "model").String(); envelopeModel != "" {
				model = envelopeModel
			}
			updated, _, err := prompts.ApplyForSendModel(c, account, []byte(inner.Raw), "gemini", false, model)
			if err != nil {
				return nil, err
			}
			if bytes.Equal(updated, []byte(inner.Raw)) {
				return body, nil
			}
			return sjson.SetRawBytes(body, field, updated)
		}
	}
	updated, _, err := prompts.ApplyForSendModel(c, account, body, "gemini", false, model)
	return updated, err
}

// applyGeminiPromptToHTTPRequest receives a freshly built request on every
// attempt. Its body is never assigned back to the clean conversion input.
func applyGeminiPromptToHTTPRequest(prompts *BusinessSystemPromptService, c *gin.Context, account *Account, req *http.Request, model string) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, fmt.Errorf("empty Gemini upstream request")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	updated, err := applyGeminiPromptForSend(prompts, c, account, body, model)
	if err != nil {
		return nil, err
	}
	// AI Studio countTokens accepts system instructions only inside its
	// generateContentRequest. Vertex countTokens uses the native top-level
	// systemInstruction schema, identified by its final publisher URL.
	if req.URL != nil && strings.HasSuffix(req.URL.Path, ":countTokens") && !strings.Contains(req.URL.Path, "/publishers/") &&
		!gjson.GetBytes(updated, "generateContentRequest").Exists() && gjson.GetBytes(updated, "systemInstruction").Exists() {
		modelResource := model
		if !strings.HasPrefix(modelResource, "models/") {
			modelResource = "models/" + modelResource
		}
		inner, err := sjson.SetBytes(updated, "model", modelResource)
		if err != nil {
			return nil, err
		}
		updated, err = json.Marshal(map[string]json.RawMessage{"generateContentRequest": inner})
		if err != nil {
			return nil, err
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(updated))
	req.ContentLength = int64(len(updated))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(updated)), nil
	}
	return updated, nil
}

func (s *AntigravityGatewayService) buildPromptedAPIRequest(ctx context.Context, c *gin.Context, account *Account, baseURL, action, token string, body []byte) (*http.Request, error) {
	updated, err := applyGeminiPromptForSend(s.businessPromptService, c, account, body, gjson.GetBytes(body, "model").String())
	if err != nil {
		return nil, err
	}
	if baseURL == "" {
		return antigravity.NewAPIRequest(ctx, action, token, updated)
	}
	return antigravity.NewAPIRequestWithURL(ctx, baseURL, action, token, updated)
}
