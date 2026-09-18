package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/klauspost/compress/zstd"
)

// 真实 Codex 客户端（codex-rs http-client）只对 ChatGPT 后端的流式 /responses 请求体做
// zstd level 3 压缩并带 Content-Encoding: zstd；compact、models 等请求保持明文。网关在最终
// 发送前对 OAuth-like 账号的同类请求做同样处理，并且只改写发出的副本：调用方持有的请求
// 对象保持明文，推理恢复、诊断快照和重试路径继续按 JSON 读取。

// codexRequestZstdWindowSize 与 zstd level 3 的默认 windowLog=21 一致。
const codexRequestZstdWindowSize = 1 << 21

// codexRequestZstdEncoders 复用流式编码器，避免每次请求分配窗口缓冲。帧形态与官方客户端
// （Rust `zstd::stream::encode_all(body, 3)`）的差异如实记录：两者都不写校验和、窗口 2MB；
// klauspost 对能一次编码完的请求体（小于一个块、约 128KB 以内）会在帧头写入内容长度并使用
// single-segment，Rust 流式编码器不写内容长度。这是合法的 zstd 帧差异，不影响解压。
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

// codexRequestZstd 是 gateway.openai_codex_request_zstd 的进程级快照：用量探针
// （AccountUsageService）拿不到网关配置，却同样向 /backend-api/codex/responses 发 OAuth POST，
// 必须与推理面用同一套压缩策略出站。与 codexForceCLI 同一模式，由持有配置的网关服务在构造时发布。
var codexRequestZstd atomic.Bool

// SetCodexRequestZstdEnabled 由持有配置的网关服务在构造时发布。
func SetCodexRequestZstdEnabled(enabled bool) {
	codexRequestZstd.Store(enabled)
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
// zstd 压缩、Content-Encoding/Content-Length 已改写的克隆；其余情况原样返回。
//
// 明文来源：可重放请求（GetBody 非空）从 GetBody 的独立副本取明文，原请求 Body 完全不动；
// 不可重放请求（GetBody 已被 PrepareRequest 清空）只能读取 Body，读取成功后用同一份明文
// 重新填充。读取失败时绝不把已读出的前缀当作完整请求发送：可重放请求退回原请求明文发送，
// 不可重放请求显式返回错误（该请求本就无法完整发出）。编码失败一律退回明文。
func (s *OpenAIGatewayService) prepareOpenAICodexWireRequest(req *http.Request, account *Account) (*http.Request, error) {
	var cfg *config.Config
	if s != nil {
		cfg = s.cfg
	}
	return prepareOpenAICodexWireRequestWithConfig(cfg, req, account)
}

// prepareOpenAICodexWireRequestWithConfig 是不依赖网关服务接收者的同一实现：账号测试等持有
// 配置但不持有网关服务的发送边界复用它，开关取自传入配置（nil 或关闭时原样返回）。
func prepareOpenAICodexWireRequestWithConfig(cfg *config.Config, req *http.Request, account *Account) (*http.Request, error) {
	if cfg == nil || !cfg.Gateway.OpenAICodexRequestZstd {
		return req, nil
	}
	return prepareOpenAICodexWireRequestUngated(req, account)
}

// prepareOpenAICodexWireRequestSnapshot 与上面语义相同，但开关取自进程级快照，供拿不到配置的
// 发送边界（用量探针）使用。
func prepareOpenAICodexWireRequestSnapshot(req *http.Request, account *Account) (*http.Request, error) {
	if !codexRequestZstd.Load() {
		return req, nil
	}
	return prepareOpenAICodexWireRequestUngated(req, account)
}

// prepareOpenAICodexWireRequestUngated 是开关已判定为开启后的公共实现：只处理 OAuth-like 账号
// 发往 /backend-api/codex/responses 的 JSON POST，其余请求原样返回。
func prepareOpenAICodexWireRequestUngated(req *http.Request, account *Account) (*http.Request, error) {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return req, nil
	}
	if !isCodexStreamingResponsesRequest(req) || req.Body == nil || req.Body == http.NoBody {
		return req, nil
	}
	if strings.TrimSpace(req.Header.Get("Content-Encoding")) != "" {
		return req, nil
	}
	if contentType := strings.ToLower(req.Header.Get("Content-Type")); contentType != "" && !strings.Contains(contentType, "json") {
		return req, nil
	}
	var raw []byte
	if req.GetBody != nil {
		snapshot, err := req.GetBody()
		if err != nil {
			slog.Debug("codex_request_zstd_skipped", "reason", "snapshot_body", "error", err)
			return req, nil
		}
		raw, err = io.ReadAll(snapshot)
		_ = snapshot.Close()
		if err != nil {
			slog.Debug("codex_request_zstd_skipped", "reason", "read_snapshot", "error", err)
			return req, nil
		}
	} else {
		var err error
		raw, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("codex request zstd: read request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
	}
	if len(raw) == 0 {
		return req, nil
	}
	compressed, err := compressCodexRequestBodyZstd(raw)
	if err != nil {
		slog.Debug("codex_request_zstd_skipped", "reason", "encode", "error", err)
		return req, nil
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
	return wire, nil
}

// doOpenAICodexUpstream 是 OpenAI 网关所有 Responses 端点 POST 的统一发送入口：先按上述
// 规则生成线上请求（满足条件时压缩），再交给 httpUpstream。读取失败作为传输错误返回。
func (s *OpenAIGatewayService) doOpenAICodexUpstream(req *http.Request, account *Account, proxyURL string) (*http.Response, error) {
	wire, err := s.prepareOpenAICodexWireRequest(req, account)
	if err != nil {
		return nil, err
	}
	return s.httpUpstream.Do(wire, proxyURL, account.ID, account.Concurrency)
}
