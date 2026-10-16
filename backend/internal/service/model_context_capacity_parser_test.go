package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAntigravityCatalogMaxTokensIsProviderSpecific(t *testing.T) {
	raw := json.RawMessage(`{"maxTokens":1048576,"maxOutputTokens":65536}`)
	agy := ParseUpstreamModelContextCapacity(raw, PlatformAntigravity)
	require.EqualValues(t, 1048576, agy.ContextWindow)
	require.EqualValues(t, 65536, agy.MaxOutputTokens)
	compatible := ParseUpstreamModelContextCapacity(raw, PlatformOpenAI)
	require.Zero(t, compatible.ContextWindow, "arbitrary compatible catalogs cannot assign a meaning to maxTokens")
	require.EqualValues(t, 65536, compatible.MaxOutputTokens)
}
