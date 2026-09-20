package service

import (
	"bytes"
	"context"
	"errors"
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
	enabled := true
	body := []byte(`{"model":"gpt-5.5","input":"` + strings.Repeat("hello ", 200) + `","stream":true}`)
	newReq := func(path string) *http.Request {
		req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com"+path, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		return req.WithContext(withCodexTransportFixture(req.Context(), enabled))
	}
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	req := newReq("/backend-api/codex/responses")
	wire, err := svc.prepareOpenAICodexWireRequest(req, oauth)
	require.NoError(t, err)
	require.NotSame(t, req, wire)
	require.Equal(t, "zstd", wire.Header.Get("Content-Encoding"))
	compressed, err := io.ReadAll(wire.Body)
	require.NoError(t, err)
	require.Equal(t, int64(len(compressed)), wire.ContentLength)
	require.Less(t, len(compressed), len(body))
	require.Equal(t, []byte{0x28, 0xb5, 0x2f, 0xfd}, compressed[:4], "zstd magic")
	require.Zero(t, compressed[4]&0x04, "帧头不写校验和（与官方客户端一致）")
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
	same, err := svc.prepareOpenAICodexWireRequest(compact, oauth)
	require.NoError(t, err)
	require.Same(t, compact, same)
	apiKey := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	same, err = svc.prepareOpenAICodexWireRequest(req, apiKey)
	require.NoError(t, err)
	require.Same(t, req, same)
	enabled = false
	disabled := newReq("/backend-api/codex/responses")
	same, err = svc.prepareOpenAICodexWireRequest(disabled, oauth)
	require.NoError(t, err)
	require.Same(t, disabled, same)

	// Quota and gateway paths consult the same domain policy.
	enabled = true
	snapshotWire, err := prepareCodexTransport(newReq("/backend-api/codex/responses"), oauth)
	require.NoError(t, err)
	require.Equal(t, "zstd", snapshotWire.Header.Get("Content-Encoding"))
	snapshotCompressed, err := io.ReadAll(snapshotWire.Body)
	require.NoError(t, err)
	require.Equal(t, body, zstdDecodeForTest(t, snapshotCompressed))
	enabled = false
	snapshotOff := newReq("/backend-api/codex/responses")
	same, err = prepareCodexTransport(snapshotOff, oauth)
	require.NoError(t, err)
	require.Same(t, snapshotOff, same)
}

// zstdDecodeForTest 解压 zstd 压缩的请求体，供断言「压缩后语义不变」的 fixture 复用。
func zstdDecodeForTest(t *testing.T, compressed []byte) []byte {
	t.Helper()
	dec, err := zstd.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	defer dec.Close()
	decoded, err := io.ReadAll(dec)
	require.NoError(t, err)
	return decoded
}

type prefixThenErrorReader struct {
	prefix []byte
	err    error
	done   bool
}

func (r *prefixThenErrorReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.prefix), nil
	}
	return 0, r.err
}

// 读取失败时绝不发送截断前缀：不可重放请求显式报错，可重放请求原请求不动、退回明文。
func TestPrepareOpenAICodexWireRequestNeverSendsTruncatedBody(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.OpenAICodexRequestZstd = true
	oauth := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	readErr := errors.New("connection reset while reading body")

	broken, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	broken.Header.Set("Content-Type", "application/json")
	broken = broken.WithContext(withCodexTransportFixture(context.Background(), true))
	broken.Body = io.NopCloser(&prefixThenErrorReader{prefix: []byte(`{"model":"gpt-5.5",`), err: readErr})
	broken.GetBody = nil
	_, err = svc.prepareOpenAICodexWireRequest(broken, oauth)
	require.ErrorIs(t, err, readErr, "不可重放请求读取失败必须显式报错")
	require.Empty(t, broken.Header.Get("Content-Encoding"))

	replayable, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader([]byte(`{"model":"gpt-5.5"}`)))
	require.NoError(t, err)
	replayable.Header.Set("Content-Type", "application/json")
	replayable = replayable.WithContext(withCodexTransportFixture(context.Background(), true))
	replayable.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(&prefixThenErrorReader{prefix: []byte(`{"mo`), err: readErr}), nil
	}
	same, err := svc.prepareOpenAICodexWireRequest(replayable, oauth)
	require.NoError(t, err)
	require.Same(t, replayable, same, "可重放请求快照失败时原请求原样明文发送")
	plain, err := io.ReadAll(replayable.Body)
	require.NoError(t, err)
	require.Equal(t, `{"model":"gpt-5.5"}`, string(plain))
	require.Empty(t, replayable.Header.Get("Content-Encoding"))
}

// WS 握手客户端不再让 Go 自动附加 Accept-Encoding: gzip。
func TestOpenAIWSDirectHTTPClientDisablesCompression(t *testing.T) {
	transport, ok := openAIWSDirectHTTPClient().Transport.(*http.Transport)
	require.True(t, ok)
	require.True(t, transport.DisableCompression)
}
