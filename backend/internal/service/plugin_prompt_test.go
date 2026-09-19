package service

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"

	policy "github.com/HTExplicit/sub2api-plugins/promptskills/policy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPromptPluginPlanKeepsLargeClientHistoryOutOfRPC(t *testing.T) {
	history := strings.Repeat("history", extensionv1.MaxPayloadBytes/7+1)
	body := []byte(`{"instructions":"client","input":"` + history + `"}`)
	snapshot := BusinessSystemPromptSnapshot{Enabled: true, Body: "server"}
	invoked := 0
	updated, application, err := applyBusinessSystemPromptWithInvoker(context.Background(), body, snapshot, BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses}, func(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
		invoked++
		require.Less(t, len(in.Payload), extensionv1.MaxPayloadBytes)
		require.NotContains(t, string(in.Payload), "history")
		return policy.New().Invoke(ctx, in)
	})
	require.NoError(t, err)
	require.Equal(t, 1, invoked)
	require.True(t, application.Applied)
	require.Equal(t, "client\n\nserver", gjson.GetBytes(updated, "instructions").String())
	require.Equal(t, history, gjson.GetBytes(updated, "input").String())
}

func TestPromptPluginCancellationUsesIncomingContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := applyBusinessSystemPromptWithInvoker(ctx, []byte(`{"input":"test"}`), BusinessSystemPromptSnapshot{Enabled: true, Body: "server"}, BusinessSystemPromptTarget{Platform: PlatformOpenAI}, func(call context.Context, _, _ string, _ extensionv1.Invocation) (extensionv1.Result, error) {
		require.ErrorIs(t, call.Err(), context.Canceled)
		return extensionv1.Result{}, call.Err()
	})
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestPromptRetryRequiresTheOriginalValidatedSnapshot(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "current-server"}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(businessSystemPromptRequestApplicationKey+":"+BusinessSystemPromptProtocolResponses, businessSystemPromptRequestState{application: BusinessSystemPromptApplication{Applied: true, Carrier: BusinessSystemPromptCarrierInstructions, ServerInstructions: "derived-server", Revision: 1}})
	_, _, err := gateway.applyBusinessSystemPromptForRequest(c, []byte(`{"instructions":"different-client"}`), &Account{Platform: PlatformOpenAI}, BusinessSystemPromptProtocolResponses, false)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable, "missing request snapshots cannot be reconstructed from result metadata")
}
