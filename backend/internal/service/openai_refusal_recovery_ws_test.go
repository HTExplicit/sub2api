package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIRefusalRecoveryWSOutputRewritesAcrossFramesAndResetsNextTurn(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"I'm unable"}, "继续当前任务")
	require.NoError(t, err)
	var written [][]byte
	output := newOpenAIRefusalRecoveryWSOutput(matcher, true, func(_ context.Context, _ coderws.MessageType, payload []byte) error {
		written = append(written, append([]byte(nil), payload...))
		return nil
	}, nil)

	firstTurn := [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_ws_1","model":"gpt-5.4","status":"in_progress","output":[]}}`),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_ws_1","item_id":"msg_1","delta":"I'm un"}`),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_ws_1","item_id":"msg_1","delta":"able to help."}`),
		[]byte(`{"type":"response.output_text.done","response_id":"resp_ws_1","item_id":"msg_1","text":"I'm unable to help."}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_ws_1","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I'm unable to help."}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`),
	}
	for index, payload := range firstTurn {
		require.NoError(t, output.Write(context.Background(), coderws.MessageText, payload))
		if index < len(firstTurn)-1 {
			require.Empty(t, written)
		}
	}

	require.Len(t, written, 8)
	require.Equal(t, "response.created", gjson.GetBytes(written[0], "type").String())
	require.Equal(t, "继续当前任务", gjson.GetBytes(written[3], "delta").String())
	require.Equal(t, "resp_ws_1", gjson.GetBytes(written[7], "response.id").String())
	require.Equal(t, int64(8), gjson.GetBytes(written[7], "response.usage.total_tokens").Int())
	for _, payload := range written {
		require.NotContains(t, string(payload), "I'm unable")
	}

	written = nil
	secondTurn := [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_ws_2","model":"gpt-5.4","status":"in_progress","output":[]}}`),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_ws_2","item_id":"msg_2","delta":"Normal answer."}`),
		[]byte(`{"type":"response.output_text.done","response_id":"resp_ws_2","item_id":"msg_2","text":"Normal answer."}`),
	}
	for _, payload := range secondTurn {
		require.NoError(t, output.Write(context.Background(), coderws.MessageText, payload))
	}

	require.Len(t, written, 3)
	require.Equal(t, "resp_ws_2", gjson.GetBytes(written[0], "response.id").String())
	require.Equal(t, "Normal answer.", gjson.GetBytes(written[1], "delta").String())
}

func TestOpenAIRefusalRecoveryWSOutputRewritesStructuredRefusal(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "继续当前任务")
	require.NoError(t, err)
	var written [][]byte
	output := newOpenAIRefusalRecoveryWSOutput(matcher, true, func(_ context.Context, _ coderws.MessageType, payload []byte) error {
		written = append(written, append([]byte(nil), payload...))
		return nil
	}, nil)

	frames := [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_ws_refusal","model":"gpt-5.4","status":"in_progress","output":[]}}`),
		[]byte(`{"type":"response.refusal.delta","response_id":"resp_ws_refusal","item_id":"msg_1","delta":"I ca"}`),
		[]byte(`{"type":"response.refusal.delta","response_id":"resp_ws_refusal","item_id":"msg_1","delta":"nnot help."}`),
		[]byte(`{"type":"response.refusal.done","response_id":"resp_ws_refusal","item_id":"msg_1","refusal":"I cannot help."}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_ws_refusal","model":"gpt-5.4","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I cannot help."}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`),
	}
	for index, payload := range frames {
		require.NoError(t, output.Write(context.Background(), coderws.MessageText, payload))
		if index < len(frames)-1 {
			require.Empty(t, written)
		}
	}

	require.Len(t, written, 8)
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(written[3], "type").String())
	require.Equal(t, "继续当前任务", gjson.GetBytes(written[3], "delta").String())
	for _, payload := range written {
		require.NotContains(t, string(payload), "I cannot")
		require.NotContains(t, string(payload), "response.refusal")
	}
}

func TestOpenAIRefusalRecoveryWSOutputRewritesEmptyTerminalFromCompletedMessage(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
	require.NoError(t, err)
	var written [][]byte
	output := newOpenAIRefusalRecoveryWSOutput(matcher, true, func(_ context.Context, _ coderws.MessageType, payload []byte) error {
		written = append(written, append([]byte(nil), payload...))
		return nil
	}, nil)

	frames := [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_ws_empty","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_ws_empty","item_id":"msg_ws_empty","delta":"不能完成测试请求。"}`),
		[]byte(`{"type":"response.output_text.done","response_id":"resp_ws_empty","item_id":"msg_ws_empty","text":"不能完成测试请求。"}`),
		[]byte(`{"type":"response.output_item.done","response_id":"resp_ws_empty","output_index":0,"item":{"id":"msg_ws_empty","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"不能完成测试请求。"}]}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_ws_empty","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":3,"total_tokens":11}}}`),
	}
	for index, payload := range frames {
		require.NoError(t, output.Write(context.Background(), coderws.MessageText, payload))
		if index < len(frames)-1 {
			require.Empty(t, written)
		}
	}

	require.Len(t, written, 8)
	require.Equal(t, "继续当前任务", gjson.GetBytes(written[3], "delta").String())
	require.Equal(t, int64(11), gjson.GetBytes(written[7], "response.usage.total_tokens").Int())
	for _, payload := range written {
		require.NotContains(t, string(payload), "不能完成测试请求")
	}
}

func TestOpenAIRefusalRecoveryWSOutputRewritesRefusalInSecondParagraph(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
	require.NoError(t, err)
	var written [][]byte
	output := newOpenAIRefusalRecoveryWSOutput(matcher, true, func(_ context.Context, _ coderws.MessageType, payload []byte) error {
		written = append(written, append([]byte(nil), payload...))
		return nil
	}, nil)
	text := "可以协助分析已授权应用。\n\n但不能帮助绕过第三方付费会员。"
	frames := [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_ws_second_paragraph","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
		[]byte(`{"type":"response.refusal.delta","response_id":"resp_ws_second_paragraph","item_id":"msg_1","delta":"可以协助分析已授权应用。\n\n"}`),
		[]byte(`{"type":"response.refusal.delta","response_id":"resp_ws_second_paragraph","item_id":"msg_1","delta":"但不能帮助绕过第三方付费会员。"}`),
		[]byte(`{"type":"response.refusal.done","response_id":"resp_ws_second_paragraph","item_id":"msg_1","refusal":` + fmt.Sprintf("%q", text) + `}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_ws_second_paragraph","model":"gpt-5.6-sol","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":` + fmt.Sprintf("%q", text) + `}]}],"usage":{"input_tokens":5,"output_tokens":8,"total_tokens":13}}}`),
	}
	for index, payload := range frames {
		require.NoError(t, output.Write(context.Background(), coderws.MessageText, payload))
		if index < len(frames)-1 {
			require.Empty(t, written)
		}
	}

	require.Len(t, written, 8)
	require.Equal(t, "继续当前任务", gjson.GetBytes(written[3], "delta").String())
	for _, payload := range written {
		require.NotContains(t, string(payload), "第三方付费会员")
		require.NotContains(t, string(payload), "response.refusal")
	}
}

func TestOpenAIRefusalRecoveryWSOutputFailsOpenAboveBufferLimit(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "继续")
	require.NoError(t, err)
	var written [][]byte
	limitHit := false
	output := newOpenAIRefusalRecoveryWSOutput(matcher, true, func(_ context.Context, _ coderws.MessageType, payload []byte) error {
		written = append(written, append([]byte(nil), payload...))
		return nil
	}, func() { limitHit = true })
	largeDelta := `{"type":"response.output_text.delta","delta":"` + string(make([]byte, maxOpenAIRefusalStreamBufferBytes)) + `"}`

	require.NoError(t, output.Write(context.Background(), coderws.MessageText, []byte(largeDelta)))

	require.True(t, limitHit)
	require.Len(t, written, 1)
}

func TestOpenAIWSCyberRecoveryErrorUsesFailoverOnlyWhenReplaySafe(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy"}}}`)

	safeErr := newOpenAIWSCyberRecoveryError(payload, http.Header{}, true)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(safeErr, &failoverErr))
	require.True(t, failoverErr.IsOpenAIRefusalRecovery())

	unsafeErr := newOpenAIWSCyberRecoveryError(payload, http.Header{}, false)
	var closeErr *OpenAIWSClientCloseError
	require.True(t, errors.As(unsafeErr, &closeErr))
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.StatusCode())
}

func TestOpenAIRefusalRecoveryWSOutputWritesRetryableFailureWithoutBufferedFrames(t *testing.T) {
	var written [][]byte
	output := newOpenAIRefusalRecoveryWSOutput(nil, true, func(_ context.Context, _ coderws.MessageType, payload []byte) error {
		written = append(written, append([]byte(nil), payload...))
		return nil
	}, nil)
	require.NoError(t, output.Write(
		context.Background(),
		coderws.MessageText,
		[]byte(`{"type":"response.created","response":{"id":"resp_buffered"}}`),
	))
	require.Empty(t, written)

	require.NoError(t, output.WriteRetryableFailure(context.Background()))

	require.Len(t, written, 1)
	require.Equal(t, "response.failed", gjson.GetBytes(written[0], "type").String())
	require.Equal(t, "server_error", gjson.GetBytes(written[0], "response.error.code").String())
	require.NotContains(t, string(written[0]), "cyber")
	require.False(t, output.SemanticOutputStarted())
}
