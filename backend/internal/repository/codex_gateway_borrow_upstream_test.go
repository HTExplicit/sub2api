package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCodexBorrowPinnedTransportPolicy(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{OpenAIHTTP2: config.GatewayOpenAIHTTP2Config{Enabled: true}}}
	upstream := NewHTTPUpstream(cfg)
	require.Same(t, upstream, NewCodexGatewayBorrowProbeUpstream(upstream))
	manager := upstream.(*httpUpstreamService)
	require.Equal(t, upstreamProtocolModeOpenAIH2, manager.resolveProtocolMode(service.HTTPUpstreamProfileCodexBorrowSource, "", nil))
	mode := manager.resolveProtocolMode(service.HTTPUpstreamProfileCodexBorrowTarget, "", nil)
	require.Equal(t, upstreamProtocolModeOpenAIH1NoReuse, mode)
	transport, err := buildUpstreamTransport(defaultPoolSettings(cfg), nil, mode)
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	require.False(t, transport.ForceAttemptHTTP2)
	require.True(t, transport.DisableKeepAlives)
	require.Zero(t, transport.MaxIdleConns)
	require.Zero(t, transport.MaxIdleConnsPerHost)
	require.Empty(t, transport.TLSNextProto)
	cfg.Gateway.OpenAIHTTP2.Enabled = false
	require.Equal(t, upstreamProtocolModeOpenAIH1, manager.resolveProtocolMode(service.HTTPUpstreamProfileCodexBorrowSource, "", nil))
}
