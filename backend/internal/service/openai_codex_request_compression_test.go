package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// 只有 OAuth-like 账号发往 /backend-api/codex/responses 的 POST 才在发送副本上做 zstd；
// 调用方持有的请求保持明文可重读，compact 与 API Key 账号不压缩。
func TestPrepareOpenAICodexWireRequestCompressesOnlyCodexStreamingTurns(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.OpenAICodexRequestZstd = true
	body := []byte(`{"model":"gpt-5.5","input":"` + strings.Repeat("hello ", 200) + `","stream":true}`)
	newReq := func(path string) *http.Request {
		req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com"+path, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	req := newReq("/backend-api/codex/responses")
	wire := svc.prepareOpenAICodexWireRequest(req, oauth)
	require.NotSame(t, req, wire)
	require.Equal(t, "zstd", wire.Header.Get("Content-Encoding"))
	compressed, err := io.ReadAll(wire.Body)
	require.NoError(t, err)
	require.Equal(t, int64(len(compressed)), wire.ContentLength)
	require.Less(t, len(compressed), len(body))
	dec, err := zstd.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	decoded, err := io.ReadAll(dec)
	dec.Close()
	require.NoError(t, err)
	require.Equal(t, body, decoded)
	require.NotNil(t, wire.GetBody)
	replay, err := wire.GetBody()
	require.NoError(t, err)
	replayed, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, compressed, replayed)

	require.Empty(t, req.Header.Get("Content-Encoding"), "原请求保持明文")
	original, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, original)

	compact := newReq("/backend-api/codex/responses/compact")
	require.Same(t, compact, svc.prepareOpenAICodexWireRequest(compact, oauth))
	apiKey := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.Same(t, req, svc.prepareOpenAICodexWireRequest(req, apiKey))
	svc.cfg.Gateway.OpenAICodexRequestZstd = false
	disabled := newReq("/backend-api/codex/responses")
	require.Same(t, disabled, svc.prepareOpenAICodexWireRequest(disabled, oauth))
}

// WS 握手客户端不再让 Go 自动附加 Accept-Encoding: gzip。
func TestOpenAIWSDirectHTTPClientDisablesCompression(t *testing.T) {
	transport, ok := openAIWSDirectHTTPClient().Transport.(*http.Transport)
	require.True(t, ok)
	require.True(t, transport.DisableCompression)
}
