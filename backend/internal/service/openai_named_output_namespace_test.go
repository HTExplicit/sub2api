package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesStandaloneOutputNamespacePolicy(t *testing.T) {
	ordinary := newOpenAIRejectedFieldTestAccount()
	cindy := newOpenAIRejectedFieldTestAccount()
	cindy.Credentials["base_url"] = "https://api.laxarouter.ai"
	for _, tt := range []struct {
		name    string
		account *Account
		compact bool
		want    bool
	}{
		{"ordinary_native", ordinary, false, true},
		{"ordinary_compact", ordinary, true, false},
		{"oauth", newOpenAIOAuthNamespaceTestAccount(), false, false},
		{"legacy_cindy", cindy, false, false},
		{"no_account", nil, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldKeepOpenAIResponsesStandaloneOutputNamespaces(tt.account, tt.compact))
		})
	}
}

func TestOpenAIResponsesNamedOutputNamespacesKeepOnlyStandaloneIdentity(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":[
		{"type":"function_call_output","name":"external_input","namespace":"original","output":"keep","extension":9007199254740993},
		{"type":"function_call_output","name":"external_input","namespace":null,"call_id":null,"output":"keep null"},
		{"type":"function_call_output","name":"external_input","namespace":"empty-id","call_id":"","output":"keep empty"},
		{"type":"function_call_output","name":"real_output","namespace":"legacy","call_id":"call-1","output":"real"},
		{"type":"function_call_output","namespace":"unnamed","output":"unknown"},
		{"type":"message","role":"user","namespace":"residual","content":"user"}
	]}`)
	got, err := stripOpenAIResponsesInputNamespaces(body, true, true)
	require.NoError(t, err)
	for _, index := range []string{"0", "1", "2"} {
		require.Equal(t, gjson.GetBytes(body, "input."+index).Raw, gjson.GetBytes(got, "input."+index).Raw)
	}
	for _, index := range []string{"3", "4", "5"} {
		require.False(t, gjson.GetBytes(got, "input."+index+".namespace").Exists())
	}
	require.Equal(t, "call-1", gjson.GetBytes(got, "input.3.call_id").String())
	again, err := stripOpenAIResponsesInputNamespaces(got, true, true)
	require.NoError(t, err)
	require.Equal(t, got, again)

	legacy, err := stripOpenAIResponsesInputNamespaces(body, true, false)
	require.NoError(t, err)
	for _, item := range gjson.GetBytes(legacy, "input").Array() {
		require.False(t, item.Get("namespace").Exists())
	}
}

func TestOpenAIGatewayForwardPreservesNamedStandaloneOutputNamespaces(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","stream":false,"store":false,"input":[
				{"type":"function_call_output","name":"external_input","namespace":"custom_namespace","output":"standalone","extension":9007199254740993},
				{"type":"function_call_output","name":"external_input","namespace":null,"output":"standalone null"}
			]}`)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
			}}
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra = map[string]any{"openai_passthrough": passthrough}
			result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
				context.Background(), newOpenAIRejectedFieldTestContext(body), account, body,
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			for _, index := range []string{"0", "1"} {
				require.JSONEq(t, gjson.GetBytes(body, "input."+index).Raw, gjson.GetBytes(upstream.bodies[0], "input."+index).Raw)
			}
			require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[0], "input.0.extension").Raw)
		})
	}
}
