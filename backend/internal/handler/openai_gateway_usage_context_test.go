package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "request-456")

	var gotClientRequestID string
	var gotRequestID string
	h := &GatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	})

	require.Equal(t, "client-request-123", gotClientRequestID)
	require.Equal(t, "request-456", gotRequestID)
}

func TestOpenAISubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "openai-client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "openai-request-456")

	var gotClientRequestID string
	var gotRequestID string
	h := &OpenAIGatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	})

	require.Equal(t, "openai-client-request-123", gotClientRequestID)
	require.Equal(t, "openai-request-456", gotRequestID)
}

func TestSnapshotOpenAIUsageMetadataOwnsValuesBeforeAsyncDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	context.Request.Header.Set("User-Agent", "snapshot-agent")
	context.Request.Header.Set("X-Forwarded-For", "192.0.2.10")
	context.Set("requested_public_model", "gpt-5")
	apiKey := &service.APIKey{ID: 7, User: &service.User{ID: 8}, Group: &service.Group{ID: 9, Platform: service.PlatformCindy}}
	account := &service.Account{ID: 10, Platform: service.PlatformCindy, Credentials: map[string]any{"api_key": "secret"}}
	result := &service.OpenAIForwardResult{UpstreamModel: "gpt-5.6-sol"}
	mapping := service.ChannelMappingResult{ChannelID: 11, Mapped: true, MappedModel: "gpt-5.6-sol", BillingModelSource: service.BillingModelSourceChannelMapped}

	snapshot := snapshotOpenAIUsageMetadata(context, apiKey, account, nil, mapping, "gpt-5", result, []byte(`{"model":"gpt-5"}`))
	apiKey.ID = 99
	account.ID = 100
	account.Credentials["api_key"] = "changed"
	context.Request.Header.Set("User-Agent", "changed-agent")
	input := snapshot.Input(result, nil, time.Time{})

	require.Equal(t, int64(7), input.APIKey.ID)
	require.Equal(t, int64(10), input.Account.ID)
	require.Equal(t, "snapshot-agent", input.UserAgent)
	require.Equal(t, int64(11), input.ChannelID)
	require.Equal(t, "gpt-5.6-sol", input.ChannelMappedModel)
	require.Equal(t, "secret", input.Account.Credentials["api_key"])
}
