package service

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptObservationContainsOnlyDerivedPromptMetadata(t *testing.T) {
	application := BusinessSystemPromptApplication{
		Applied:                     true,
		Revision:                    11,
		EffectiveSHA256:             "ABCDEF",
		BundlePromptEffectiveSHA256: "1234ABCD",
		Carrier:                     BusinessSystemPromptCarrierInstructions,
		ClientInstructions:          "client secret instructions",
		ServerInstructions:          "server secret instructions",
	}

	observation := newBusinessSystemPromptObservation(
		application,
		OpenAIClientTransportHTTP,
		OpenAIUpstreamTransportResponsesWebsocketV2,
		openAICindyHTTPToWSV2Reason,
	)
	merged := MergeBusinessSystemPromptInstructions(application.ClientInstructions, application.ServerInstructions)
	digest := sha256.Sum256([]byte(merged))

	require.True(t, observation.Applied)
	require.Equal(t, int64(11), observation.Revision)
	require.Equal(t, "abcdef", observation.EffectiveSHA256)
	require.Equal(t, "1234abcd", observation.BundlePromptEffectiveHash)
	require.Equal(t, len([]byte(application.ClientInstructions)), observation.ClientBytes)
	require.Equal(t, len([]byte(application.ServerInstructions)), observation.ServerBytes)
	require.Equal(t, len([]byte(merged)), observation.MergedBytes)
	require.Equal(t, hex.EncodeToString(digest[:]), observation.MergedSHA256)
	require.Equal(t, "http", observation.IngressTransport)
	require.Equal(t, "responses_websockets_v2", observation.UpstreamTransport)
	require.Equal(t, openAICindyHTTPToWSV2Reason, observation.SelectionReason)
}

func TestBusinessSystemPromptObservationDoesNotHashAbsentPrompt(t *testing.T) {
	observation := newBusinessSystemPromptObservation(
		BusinessSystemPromptApplication{},
		OpenAIClientTransportHTTP,
		OpenAIUpstreamTransportHTTPSSE,
		"client_protocol_http",
	)

	require.False(t, observation.Applied)
	require.Empty(t, observation.MergedSHA256)
	require.Zero(t, observation.MergedBytes)
}
