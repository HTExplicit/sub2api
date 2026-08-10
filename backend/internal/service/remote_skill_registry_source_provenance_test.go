package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillVersionFromDescriptorUsesHistoricalGitHubCommitRoot(t *testing.T) {
	commit := strings.Repeat("a", 40)
	version := remoteSkillVersionFromDescriptor(RemoteSkillPublicDescriptor{
		SourceID:     RemoteSkillSourceGitHubOfficial,
		SourceCommit: commit,
	})

	require.Equal(t, remoteSkillVersionSourceRoot(RemoteSkillSourceGitHubOfficial, commit), version.RemoteRoot)
	require.NotEqual(t, RemoteSkillGitHubRoot, version.RemoteRoot)
}
