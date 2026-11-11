package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newSystemPromptResponsesRetryService(upstream *httpUpstreamRecorder, enabled bool, role string) *OpenAIGatewayService {
	svc := newOpenAIRejectedFieldTestService(upstream)
	svc.systemPrompts = &SystemPromptService{}
	svc.systemPrompts.publish(SystemPromptConfig{Enabled: enabled, DefaultPromptID: "site", Prompts: []SystemPrompt{
		{ID: "site", Name: "site", Body: "site instructions", Position: SystemPromptPositionPrepend, Role: role},
	}})
	return svc
}

func TestSystemPromptResponsesRetryHTTPActualInputIndices(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough_%t/prompt_%t", passthrough, enabled), func(t *testing.T) {
				body := []byte(`{"model":"gpt-5.5","stream":false,"store":true,"input":[{"type":"message","role":"user","status":"keep","content":"hello"},{"type":"reasoning","status":"completed","summary":[]}]}`)
				clientBody := append([]byte(nil), body...)
				reasoningIndex := 1
				if enabled {
					reasoningIndex++
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, fmt.Sprintf(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[%d].status'.","param":"input[%d].status"}}`, reasoningIndex, reasoningIndex)),
					newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_prompt_retry","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
				}}
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = passthrough
				svc := newSystemPromptResponsesRetryService(upstream, enabled, SystemPromptRoleDeveloper)

				result, err := svc.Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), account, body)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 2, "the official indexed-field repair must retry the actual sent body")
				for _, sent := range upstream.bodies {
					input := gjson.GetBytes(sent, "input").Array()
					require.Len(t, input, reasoningIndex+1, "a retry must not inject a second developer message")
					if enabled {
						require.Equal(t, "developer", input[0].Get("role").String())
						require.Equal(t, "site instructions", input[0].Get("content.0.text").String())
					}
					require.Equal(t, "keep", input[reasoningIndex-1].Get("status").String())
					require.Equal(t, "hello", input[reasoningIndex-1].Get("content").String())
					require.Equal(t, "reasoning", input[reasoningIndex].Get("type").String())
				}
				require.True(t, gjson.GetBytes(upstream.bodies[0], fmt.Sprintf("input.%d.status", reasoningIndex)).Exists())
				require.False(t, gjson.GetBytes(upstream.bodies[1], fmt.Sprintf("input.%d.status", reasoningIndex)).Exists())
				require.Equal(t, clientBody, body, "the client body remains available for a later account attempt")
			})
		}
	}
}

func TestSystemPromptResponsesRetryHTTPInstructionsEcho(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, clientInstructions := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough_%t/client_instructions_%t", passthrough, clientInstructions), func(t *testing.T) {
				body := []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":128,"input":"hello"}`)
				finalInstructions := "site instructions"
				if clientInstructions {
					body = []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":128,"input":"hello","instructions":"client instructions"}`)
					finalInstructions += "\n\nclient instructions"
				}
				response, err := json.Marshal(map[string]any{"id": "resp_prompt_echo", "instructions": finalInstructions, "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
				require.NoError(t, err)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens"}}`),
					newOpenAIRejectedFieldTestResponse(http.StatusOK, string(response)),
				}}
				account := newOpenAIRejectedFieldTestAccount()
				account.Extra["openai_passthrough"] = passthrough
				svc := newSystemPromptResponsesRetryService(upstream, true, SystemPromptRoleAuto)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

				result, err := svc.Forward(context.Background(), c, account, body)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 2)
				for _, sent := range upstream.bodies {
					require.Equal(t, finalInstructions, gjson.GetBytes(sent, "instructions").String(), "the repaired request reuses the once-injected instructions")
				}
				require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
				echo := gjson.Get(recorder.Body.String(), "instructions")
				require.True(t, echo.Exists())
				if clientInstructions {
					require.Equal(t, "client instructions", echo.String())
				} else {
					require.Equal(t, gjson.Null, echo.Type)
				}
			})
		}
	}
}

func TestSystemPromptResponsesRetryHTTPBridge(t *testing.T) {
	tests := []struct {
		name               string
		role               string
		input              string
		rejection          string
		finalInstructions  string
		clientInstructions bool
	}{
		{
			name: "developer uses sent index", role: SystemPromptRoleDeveloper,
			input:     `"input":[{"type":"message","role":"user","status":"keep","content":"hello"},{"type":"reasoning","status":"completed","summary":[]}]`,
			rejection: `{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[2].status'.","param":"input[2].status"}}`,
		},
		{
			name: "auto restores omitted instructions", role: SystemPromptRoleAuto,
			input: `"input":"hello","max_output_tokens":128`, finalInstructions: "site instructions",
			rejection: `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens"}}`,
		},
		{
			name: "auto restores client instructions", role: SystemPromptRoleAuto,
			input: `"input":"hello","max_output_tokens":128,"instructions":"client instructions"`, finalInstructions: "site instructions\n\nclient instructions", clientInstructions: true,
			rejection: `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`{"type":"response.create","model":"gpt-5.5","store":true,` + tt.input + `}`)
			response := map[string]any{"id": "resp_bridge_prompt_retry", "status": "completed", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}
			if tt.role == SystemPromptRoleAuto {
				response["instructions"] = tt.finalInstructions
			}
			event, err := json.Marshal(map[string]any{"type": "response.completed", "response": response})
			require.NoError(t, err)
			completed := newOpenAIRejectedFieldTestResponse(http.StatusOK, "data: "+string(event)+"\n\n")
			completed.Header.Set("Content-Type", "text/event-stream")
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, tt.rejection), completed,
			}}
			svc := newSystemPromptResponsesRetryService(upstream, true, tt.role)
			c := newOpenAIRejectedFieldTestContext(payload)
			var writes [][]byte

			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, newOpenAIRejectedFieldTestAccount(), "sk-test", payload, len(payload), "gpt-5.5", "", "", "", "", 1,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				})

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 2)
			require.Len(t, writes, 1, "the repaired rejection is not exposed as a client event")
			require.Equal(t, "response.completed", gjson.GetBytes(writes[0], "type").String())
			if tt.role == SystemPromptRoleDeveloper {
				for _, sent := range upstream.bodies {
					require.Len(t, gjson.GetBytes(sent, "input").Array(), 3)
					require.Equal(t, "site instructions", gjson.GetBytes(sent, "input.0.content.0.text").String())
					require.Equal(t, "keep", gjson.GetBytes(sent, "input.1.status").String())
				}
				require.True(t, gjson.GetBytes(upstream.bodies[0], "input.2.status").Exists())
				require.False(t, gjson.GetBytes(upstream.bodies[1], "input.2.status").Exists())
			} else {
				for _, sent := range upstream.bodies {
					require.Equal(t, tt.finalInstructions, gjson.GetBytes(sent, "instructions").String())
				}
				require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
				echo := gjson.GetBytes(writes[0], "response.instructions")
				require.True(t, echo.Exists())
				if tt.clientInstructions {
					require.Equal(t, "client instructions", echo.String())
				} else {
					require.Equal(t, gjson.Null, echo.Type)
				}
			}
		})
	}
}
