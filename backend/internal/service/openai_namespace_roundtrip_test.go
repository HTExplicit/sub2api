package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type namespaceRoundtripUpstream struct {
	httpUpstreamRecorder
	namespace json.RawMessage
	missing   bool
}

func (u *namespaceRoundtripUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	callSeen := false
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "function_call" {
			callSeen = true
			value := item.Get("namespace")
			u.missing = !value.Exists() || value.Raw != string(u.namespace)
		}
		return true
	})
	response := fmt.Sprintf(`{"id":"resp_namespace_fixture","object":"response","status":"completed","model":"gpt-6-astra","output":[{"type":"reasoning","id":"rs_fixture","encrypted_content":"local-only-cipher","summary":[]},{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","name":"read_state","namespace":%s,"arguments":"{}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1}}`, u.namespace)
	status := http.StatusOK
	if u.missing {
		status = http.StatusBadRequest
		response = `{"error":{"type":"invalid_request_error","code":null,"param":"input[2].namespace","message":"Missing namespace for function_call. Round-trip its namespace field."}}`
	} else if callSeen {
		response = `{"id":"resp_namespace_done","object":"response","status":"completed","model":"gpt-6-astra","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	u.resp = &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}
	return u.httpUpstreamRecorder.Do(req, proxy, id, concurrency)
}

func (u *namespaceRoundtripUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestOpenAIResponsesNamespaceRoundtripPreservesModelHistory(t *testing.T) {
	for _, tc := range []struct {
		name        string
		passthrough bool
		omitTools   bool
		namespace   json.RawMessage
	}{
		{name: "native_flat", namespace: json.RawMessage(`"functions"`)},
		{name: "passthrough_flat", passthrough: true, namespace: json.RawMessage(`"functions"`)},
		{name: "native_no_tools_null", omitTools: true, namespace: json.RawMessage(`null`)},
		{name: "passthrough_no_tools_null", passthrough: true, omitTools: true, namespace: json.RawMessage(`null`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &namespaceRoundtripUpstream{namespace: tc.namespace}
			svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false)}
			account := businessSystemPromptAPIKeyAccount(true)
			account.Extra["openai_passthrough"] = tc.passthrough
			first := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"ultra"},"stream":false,"prompt_cache_key":"local-seed","tools":[{"type":"function","name":"read_state","parameters":{"type":"object","properties":{},"additionalProperties":false}}],"input":[{"role":"user","content":"local diagnostic"}]}`)
			c, rec := newBusinessSystemPromptGinContext("/v1/responses", first)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			_, err := svc.Forward(context.Background(), c, account, first)
			require.NoError(t, err)
			response := gjson.Parse(rec.Body.String())
			require.Equal(t, string(tc.namespace), response.Get("output.1.namespace").Raw)
			var next map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(first, &next))
			// Preserve each complete output item, including future fields and null.
			items := []json.RawMessage{json.RawMessage(`{"role":"user","content":"local diagnostic"}`), json.RawMessage(response.Get("output.0").Raw), json.RawMessage(response.Get("output.1").Raw), json.RawMessage(`{"type":"function_call_output","call_id":"call_fixture","output":"local result"}`)}
			next["input"], err = json.Marshal(items)
			require.NoError(t, err)
			if tc.omitTools {
				delete(next, "tools")
			}
			second, err := json.Marshal(next)
			require.NoError(t, err)
			c2, rec2 := newBusinessSystemPromptGinContext("/v1/responses", second)
			SetOpenAIClientTransport(c2, OpenAIClientTransportHTTP)
			_, err = svc.Forward(context.Background(), c2, account, second)
			require.NoError(t, err)
			require.Equal(t, "completed", gjson.Get(rec2.Body.String(), "status").String())
			require.False(t, upstream.missing)
			require.Equal(t, tc.passthrough, c2.GetBool("openai_passthrough"))
			require.Equal(t, string(tc.namespace), gjson.GetBytes(upstream.lastBody, "input.2.namespace").Raw)
			require.JSONEq(t, gjson.GetBytes(second, "input").Raw, gjson.GetBytes(upstream.lastBody, "input").Raw)
			require.Len(t, upstream.bodies, 2, "each turn must succeed on its first attempt")
		})
	}
}

func TestOpenAIResponsesNamespaceRejectionCompatibilityRemainsExact(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"function_call","id":"fc_test","call_id":"call_test","name":"read_state","namespace":"functions","arguments":"{}"},{"type":"function_call_output","call_id":"call_test","output":"ok"},{"type":"function_call","call_id":"call_other","name":"other","namespace":null,"arguments":"{}"}]}`)
	unknown := []byte(`{"error":{"type":"invalid_request_error","code":null,"message":"Unknown parameter: 'input[1].namespace'.","param":"input[1].namespace"}}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(400, string(unknown)),
		newOpenAIRejectedFieldTestResponse(200, `{"id":"resp_ok","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
	}}
	_, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestAccount(), body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "functions", gjson.GetBytes(upstream.bodies[0], "input.1.namespace").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.1.namespace").Exists())
	require.Equal(t, "null", gjson.GetBytes(upstream.bodies[1], "input.3.namespace").Raw)
	require.Equal(t, "cipher", gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").String())
	missing := []byte(`{"error":{"type":"invalid_request_error","code":null,"message":"Missing namespace for function_call.","param":"input[1].namespace"}}`)
	_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, body, missing)
	require.NoError(t, err)
	require.False(t, changed, "missing is not permission to delete a namespace")
}
