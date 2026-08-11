package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillSourceContractHasOneUpstreamAndNoCommitProvider(t *testing.T) {
	require.Equal(t, "moxinggang", RemoteSkillUpstreamSourceID)
	require.Equal(t, "https://moxinggang.com/skills/security-research/current", RemoteSkillUpstreamRoot)
	require.Equal(t, "https://codexrip.vip/skills/security-research/current", RemoteSkillPublicRoot)
}
