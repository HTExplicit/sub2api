package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSIngressReasoningModeUsesResolvedSessionModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name         string
		clientModel  string
		channelModel string
		accountModel string
		wantMode     string
		wantEffort   string
	}{
		{name: "inherited_astra", clientModel: "gpt-6-astra", accountModel: "gpt-6-astra", wantMode: "pro"},
		{name: "account_alias_astra", clientModel: "assistant", accountModel: "gpt-6-astra", wantMode: "pro"},
		{name: "channel_and_account_alias_astra", clientModel: "client-assistant", channelModel: "assistant", accountModel: "gpt-6-astra", wantMode: "pro"},
		{name: "astra_alias_targets_non_astra", clientModel: "gpt-6-astra", accountModel: "gpt-5.5", wantEffort: "max"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			controlCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 4
			captureConn := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_mode_1","status":"completed","output":[]}}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_mode_2","status":"completed","output":[]}}`),
			}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			defer pool.Close()
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			mappingSource := tt.clientModel
			if tt.channelModel != "" {
				mappingSource = tt.channelModel
			}
			account := &Account{
				ID: 916, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"access_token":  "sk-test",
					"model_mapping": map[string]any{mappingSource: tt.accountModel},
				},
				Extra: map[string]any{
					"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
				},
			}
			var mappedClientModels []string
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{MapRequestModel: func(_ int, clientModel string) (string, error) {
					mappedClientModels = append(mappedClientModels, clientModel)
					if tt.channelModel != "" {
						return tt.channelModel, nil
					}
					return clientModel, nil
				}}
			})
			defer server.Close()
			client, _, err := coderws.Dial(controlCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			payloads := []string{
				fmt.Sprintf(`{"type":"response.create","model":%q,"input":"first","reasoning":{"mode":"pro"}}`, tt.clientModel),
				`{"type":"response.create","input":"second","reasoning":{"mode":"pro"}}`,
			}
			for _, payload := range payloads {
				require.NoError(t, client.Write(controlCtx, coderws.MessageText, []byte(payload)))
				_, event, readErr := client.Read(controlCtx)
				require.NoError(t, readErr)
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-controlCtx.Done():
				t.Fatal("waiting for model-specific WS session normalization timed out")
			}
			require.Equal(t, []string{tt.clientModel, tt.clientModel}, mappedClientModels, "inherit the client ID and map exactly once per turn")
			require.Len(t, captureConn.writes, 2)
			for _, written := range captureConn.writes {
				body := requestToJSONString(written)
				require.Equal(t, tt.accountModel, gjson.Get(body, "model").String())
				require.Equal(t, tt.wantMode, gjson.Get(body, "reasoning.mode").String())
				require.Equal(t, tt.wantEffort, gjson.Get(body, "reasoning.effort").String())
			}
		})
	}
}

func TestOpenAIWSPassthroughSelectedCompatibilityUsesResolvedSessionModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name        string
		accountType string
		upstream    string
		wantMode    string
		wantEffort  string
		wantItems   int
	}{
		{name: "oauth_mapped_inherited_astra", accountType: AccountTypeOAuth, upstream: "gpt-6-astra", wantMode: "pro", wantItems: 2},
		{name: "oauth_non_astra", accountType: AccountTypeOAuth, upstream: "gpt-5.5", wantEffort: "max", wantItems: 2},
		{name: "setup_token_non_astra", accountType: AccountTypeSetupToken, upstream: "gpt-5.5", wantEffort: "max", wantItems: 2},
		{name: "ordinary_api_key_unchanged", accountType: AccountTypeAPIKey, upstream: "gpt-5.5", wantMode: "pro", wantItems: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			controlCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(cfg, upstream)
			account := passthroughLifecycleAccount()
			account.Type = tt.accountType
			account.Credentials = map[string]any{
				"api_key": "sk-test", "access_token": "sk-test",
				"model_mapping": map[string]any{"assistant": tt.upstream},
			}
			if account.Type != AccountTypeAPIKey {
				account.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough}
			}
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{MapRequestModel: func(_ int, clientModel string) (string, error) {
					return clientModel, nil
				}}
			})
			defer server.Close()
			client, _, err := coderws.Dial(controlCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			const signedItem = `{ "type" : "reasoning", "id":"rs_keep", "encrypted_content":"signed-cipher", "phase":"analysis", "extension":900719925474099312345 }`
			const namedOutput = `{"type":"function_call_output","name":"delegate","output":"standalone result"}`
			const orphanOutput = `{"type":"function_call_output","call_id":"call_missing","output":"orphan result"}`
			for turn := 1; turn <= 2; turn++ {
				modelField := `"model":"assistant",`
				if turn == 2 {
					modelField = ""
				}
				payload := fmt.Sprintf(`{"type":"response.create",%s"input":[%s,%s,%s],"reasoning":{"mode":"pro"},"extension":{"number":1.2300}}`, modelField, signedItem, namedOutput, orphanOutput)
				require.NoError(t, client.Write(controlCtx, coderws.MessageText, []byte(payload)))
				written := requirePassthroughUpstreamWrite(t, upstream, time.Second)
				require.Equal(t, tt.upstream, gjson.GetBytes(written, "model").String())
				require.Equal(t, tt.wantMode, gjson.GetBytes(written, "reasoning.mode").String())
				require.Equal(t, tt.wantEffort, gjson.GetBytes(written, "reasoning.effort").String())
				items := gjson.GetBytes(written, "input").Array()
				require.Len(t, items, tt.wantItems)
				require.Equal(t, signedItem, items[0].Raw, "selected compatibility must not reserialize raw signed history")
				require.Equal(t, namedOutput, items[1].Raw, "named standalone outputs remain intact")
				require.Equal(t, "1.2300", gjson.GetBytes(written, "extension.number").Raw)
				if tt.wantItems == 3 {
					require.Equal(t, orphanOutput, items[2].Raw)
				}
				upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_selected_%d","status":"completed","output":[]}}`, turn))
				_, event, readErr := client.Read(controlCtx)
				require.NoError(t, readErr)
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-controlCtx.Done():
				t.Fatal("waiting for selected passthrough compatibility session timed out")
			}
		})
	}
}
