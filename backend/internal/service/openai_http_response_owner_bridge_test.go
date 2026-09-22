package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIHTTPResponseOwner_CindyBridgeRegistersAuthenticatedOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_bridge_owner","model":"gpt-5.4-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t, cindyHTTPToWSV2DialStep{conn: capture})
	const groupID int64 = 61201
	c, recorder := newCindyHTTPToWSV2TurnStateTestContext("http-owner-bridge", 2002, groupID)
	// The HTTP handler supplies this authenticated identity. Its actual ingress
	// validation and stamping are exercised in openai_http_response_owner_test.go.
	SetOpenAIHTTPResponseOwner(c, 202, 2002)
	account := cindyHTTPToWSV2TestAccount()
	body := []byte(`{"model":"gpt-5.4-mini","stream":false,"input":"hello","user_id":101,"api_key_id":1001}`)

	result, err := svc.Forward(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.OpenAIWSMode, "the real HTTP-to-WSv2 success branch must execute")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "resp_bridge_owner", result.ResponseID)
	require.Len(t, dialer.capturedHeaders(), 1, "only the in-memory dialer is used")
	boundAccount, err := svc.getOpenAIWSStateStore().GetResponseAccount(context.Background(), groupID, result.ResponseID)
	require.NoError(t, err)
	require.Equal(t, account.ID, boundAccount, "response affinity alone does not authorize an HTTP continuation")

	owned, err := svc.ValidateOpenAIHTTPResponseOwner(context.Background(), groupID, result.ResponseID, 202, 2003)
	require.NoError(t, err)
	require.True(t, owned, "the HTTP bridge must register its authenticated user; another key of that user may continue")
	for _, identity := range []struct{ groupID, userID, apiKeyID int64 }{
		{groupID, 101, 1001},
		{groupID + 1, 202, 2002},
	} {
		allowed, err := svc.ValidateOpenAIHTTPResponseOwner(context.Background(), identity.groupID, result.ResponseID, identity.userID, identity.apiKeyID)
		require.NoError(t, err)
		require.False(t, allowed, "body identity and another group cannot inherit the bridge response")
	}
}
