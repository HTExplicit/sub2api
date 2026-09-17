package service

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// 真实 Codex 客户端（codex-rs http-client）只对 ChatGPT 后端的流式 /responses 请求体做
// zstd level 3 压缩并带 Content-Encoding: zstd；compact、models 等请求保持明文。网关在最终
// 发送前对 OAuth-like 账号的同类请求做同样处理，并且只改写发出的副本：调用方持有的请求
// 对象保持明文，推理恢复、诊断快照和重试路径继续按 JSON 读取。

// codexRequestZstdWindowSize 与 zstd level 3 的默认 windowLog=21 一致。
const codexRequestZstdWindowSize = 1 << 21

// codexRequestZstdEncoders 复用流式编码器：与 Rust `zstd::stream::encode_all` 一样按流写出
// （帧头不带内容长度、不带校验和），避免每次请求分配窗口缓冲。
var codexRequestZstdEncoders = sync.Pool{New: func() any {
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderCRC(false),
		zstd.WithWindowSize(codexRequestZstdWindowSize),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		return nil
	}
	return enc
}}

func compressCodexRequestBodyZstd(src []byte) ([]byte, error) {
	enc, _ := codexRequestZstdEncoders.Get().(*zstd.Encoder)
	if enc == nil {
		return nil, errors.New("zstd encoder unavailable")
	}
	defer codexRequestZstdEncoders.Put(enc)
	var buf bytes.Buffer
	buf.Grow(len(src)/3 + 64)
	enc.Reset(&buf)
	if _, err := enc.Write(src); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *OpenAIGatewayService) codexRequestZstdEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.OpenAICodexRequestZstd
}

// isCodexStreamingResponsesRequest 判断请求是否是官方客户端会压缩的那一类：
// POST 到 ChatGPT 后端 /backend-api/codex/responses（不含 /compact）。
func isCodexStreamingResponsesRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || req.Method != http.MethodPost {
		return false
	}
	path := strings.TrimSuffix(req.URL.Path, "/")
	return strings.HasSuffix(path, "/codex/responses")
}

// prepareOpenAICodexWireRequest 返回真正发往上游的请求。满足条件时返回一个请求体已
// zstd 压缩、Content-Encoding/Content-Length 已改写的克隆；其余情况原样返回。调用方
// 传入的请求在返回后仍可按明文重复读取（Body 已被重新填充）。任何失败都退回明文发送。
func (s *OpenAIGatewayService) prepareOpenAICodexWireRequest(req *http.Request, account *Account) *http.Request {
	if !s.codexRequestZstdEnabled() || account == nil || !account.IsOpenAIOAuthLike() {
		return req
	}
	if !isCodexStreamingResponsesRequest(req) || req.Body == nil || req.Body == http.NoBody {
		return req
	}
	if strings.TrimSpace(req.Header.Get("Content-Encoding")) != "" {
		return req
	}
	if contentType := strings.ToLower(req.Header.Get("Content-Type")); contentType != "" && !strings.Contains(contentType, "json") {
		return req
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		slog.Debug("codex_request_zstd_skipped", "reason", "read_body", "error", err)
		return req
	}
	if len(raw) == 0 {
		return req
	}
	compressed, err := compressCodexRequestBodyZstd(raw)
	if err != nil {
		slog.Debug("codex_request_zstd_skipped", "reason", "encode", "error", err)
		return req
	}
	wire := req.Clone(req.Context())
	wire.Body = io.NopCloser(bytes.NewReader(compressed))
	wire.ContentLength = int64(len(compressed))
	wire.Header.Set("Content-Encoding", "zstd")
	if req.GetBody != nil {
		wire.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(compressed)), nil
		}
	} else {
		wire.GetBody = nil
	}
	return wire
}
