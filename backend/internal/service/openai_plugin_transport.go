package service

import (
	"context"
	"net/http"
)

type codexWireObserverKey struct{}

func withCodexWireObserver(ctx context.Context, observer func(*http.Request)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, codexWireObserverKey{}, observer)
}

func codexWireObserverFromContext(ctx context.Context) func(*http.Request) {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(codexWireObserverKey{}).(func(*http.Request))
	return observer
}

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。未命中插件时，发往
// /backend-api/codex/responses 的 OAuth POST 与真实转发（doOpenAICodexUpstream）走同一
// zstd 压缩准备函数、同一门控；TLS 指纹 / 代理 / 凭据来源不变。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	// 插件 round-trip 有意拿到明文请求；zstd 压缩是网关传输层的事，只在真正经
	// HTTPUpstream 发出的副本上做。
	wire, err := prepareCodexTransport(request, account)
	if err != nil {
		return nil, err
	}
	if observer := codexWireObserverFromContext(wire.Context()); observer != nil {
		observer(wire)
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			wire,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(wire, proxyURL, account.ID, account.Concurrency)
}
