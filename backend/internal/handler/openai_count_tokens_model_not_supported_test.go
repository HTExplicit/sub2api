package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWriteCountTokensFailoverErrorPreservesModelNotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeCountTokensFailoverError(ctx, &service.UpstreamFailoverError{
		StatusCode: http.StatusBadRequest,
		Reason:     service.GatewayFailureReason("upstream_400_model_not_supported"),
	}, nil)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	errBody, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, service.OpenAIModelNotSupportedCode, errBody["type"])
	require.Equal(t, service.OpenAIModelNotSupportedCode, errBody["code"])
	require.Equal(t, service.OpenAIModelNotSupportedClientMessage, errBody["message"])
}
