package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// OpenAI 专用传输不再让 Go 自动附加 Accept-Encoding: gzip；默认传输保持原样。
func TestBuildUpstreamTransportDisablesCompressionForOpenAIModes(t *testing.T) {
	for _, mode := range []string{upstreamProtocolModeOpenAIH2, upstreamProtocolModeOpenAIH1, upstreamProtocolModeOpenAIH1Fallback} {
		transport, err := buildUpstreamTransport(defaultPoolSettings(nil), nil, mode)
		require.NoError(t, err, mode)
		require.True(t, transport.DisableCompression, mode)
	}
	transport, err := buildUpstreamTransport(defaultPoolSettings(nil), nil, upstreamProtocolModeDefault)
	require.NoError(t, err)
	require.False(t, transport.DisableCompression)
}
