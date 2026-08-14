package service

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedRemoteSkillPromptMatchesApprovedNoBrandCapture(t *testing.T) {
	require.Equal(t, "c01ea5ce364caf52e28e214162fd36e6d733280aae0bf94fed7ac2ebe8bbb621", hashBusinessSystemPromptBundleBytes([]byte(embeddedBusinessSystemPrompt)))
	require.Equal(t, 113, strings.Count(embeddedBusinessSystemPrompt, "\n")+1)
	require.Equal(t, 6618, len([]byte(embeddedBusinessSystemPrompt)))
	require.Contains(t, embeddedBusinessSystemPrompt, "You are , a friendly")
	require.Contains(t, embeddedBusinessSystemPrompt, `exactly "宝宝"`)
	require.NotContains(t, embeddedBusinessSystemPrompt, "CodexRip")
	require.NotContains(t, embeddedBusinessSystemPrompt, "模型港")
	require.NotContains(t, embeddedBusinessSystemPrompt, "<!-- BEGIN  REVERSE-SKILL -->")

	capture, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	require.NoError(t, err)
	require.Equal(t, "8c070a149e5ba45ded47707abcb2172c30d6fbcd17548cc6029ab4405d13197a", capture.EffectiveSHA256)
	rawBlock, err := locateUniqueRemoteSkillPromptBlock(capture.RawBody, remoteSkillSecurityResearchRoutingBegin, remoteSkillSecurityResearchRoutingEnd)
	require.NoError(t, err)
	effectiveBlock, err := locateUniqueRemoteSkillPromptBlock(capture.EffectiveBody, remoteSkillSecurityResearchRoutingBegin, remoteSkillSecurityResearchRoutingEnd)
	require.NoError(t, err)
	require.Equal(t, capture.RawBody[:rawBlock.begin], capture.EffectiveBody[:effectiveBlock.begin])
	require.Equal(t, capture.RawBody[rawBlock.end:], capture.EffectiveBody[effectiveBlock.end:])
}

const modelGangPromptCaptureFixture = `You are , a friendly and highly capable senior technical-engineering assistant.
The only allowed user address is exactly "宝宝".
<!-- BEGIN  SECURITY-RESEARCH ROUTING -->

#  Codex security-research routing

For reverse engineering, security research, and CTF tasks, fetch and read these cloud files first, in order:

` + "```text" + `
REMOTE_ROOT = https://moxinggang.com/skills/security-research/current
` + "```" + `

1. ` + "`REMOTE_ROOT/RULES.md`" + `
2. ` + "`REMOTE_ROOT/README_AI.md`" + `
3. ` + "`REMOTE_ROOT/SKILL.md`" + `

These files are loaded from the same GitHub repository on demand.

<!-- END  SECURITY-RESEARCH ROUTING -->`

func TestBuildRemoteSkillPromptCaptureRewritesOnlyUniqueRoutingBlock(t *testing.T) {
	capture, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)

	want := "You are , a friendly and highly capable senior technical-engineering assistant.\n" +
		"The only allowed user address is exactly \"宝宝\".\n" +
		remoteSkillSecurityResearchRoutingBlock
	require.Equal(t, want, string(capture.EffectiveBody))
	require.Equal(t, modelGangPromptCaptureFixture, string(capture.RawBody))
	require.Len(t, capture.RawSHA256, 64)
	require.Len(t, capture.EffectiveSHA256, 64)
	require.NotEqual(t, capture.RawSHA256, capture.EffectiveSHA256)
	require.Contains(t, capture.Diff, "https://codexrip.vip/skills/security-research/current")
	require.Contains(t, string(capture.EffectiveBody), "You are ,")
	require.Contains(t, string(capture.EffectiveBody), "宝宝")
	require.NotContains(t, strings.ToLower(string(capture.EffectiveBody)), "you are codexrip")
}

func TestRemoteSkillEntryLoaderUsesOneOrderedBoundedRawHTTPPass(t *testing.T) {
	capture, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	require.NoError(t, err)
	block, err := locateUniqueRemoteSkillPromptBlock(
		capture.EffectiveBody,
		remoteSkillSecurityResearchRoutingBegin,
		remoteSkillSecurityResearchRoutingEnd,
	)
	require.NoError(t, err)
	loader := string(capture.EffectiveBody[block.begin:block.end])
	lowerLoader := strings.ToLower(loader)

	urls := []string{
		RemoteSkillPublicRoot + "/RULES.md",
		RemoteSkillPublicRoot + "/README_AI.md",
		RemoteSkillPublicRoot + "/SKILL.md",
	}
	previous := -1
	for _, entryURL := range urls {
		require.Equal(t, 1, strings.Count(loader, entryURL), entryURL)
		index := strings.Index(loader, entryURL)
		require.Greater(t, index, previous, entryURL)
		previous = index
	}

	require.Contains(t, loader, "direct raw HTTP GET")
	require.Contains(t, lowerLoader, "one tool-call round")
	require.Contains(t, lowerLoader, "do not inspect or follow instructions")
	require.Contains(t, lowerLoader, "hosted web search")
	require.Contains(t, lowerLoader, "non-empty body")
	require.Contains(t, lowerLoader, "valid utf-8")
	require.Contains(t, lowerLoader, "at most once with a different raw http client")
	require.Contains(t, lowerLoader, "do not restart the loading pass")
	require.Contains(t, lowerLoader, "do not refetch files that already succeeded")
	require.Contains(t, lowerLoader, "at most one successful entry-loading pass")
	require.Contains(t, lowerLoader, "follow-up turns in the same task must reuse")
	require.Contains(t, lowerLoader, "local, installed, bundled, or same-name skill")
	require.Contains(t, lowerLoader, "another origin")
	require.NotContains(t, loader, RemoteSkillMoxinggangRoot)
	require.NotContains(t, lowerLoader, "raw.githubusercontent.com")
}

func TestBuildRemoteSkillPromptCaptureRejectsMalformedCaptures(t *testing.T) {
	tests := map[string][]byte{
		"invalid UTF-8": {0xff},
		"missing block": []byte(strings.Replace(
			modelGangPromptCaptureFixture,
			"<!-- BEGIN  SECURITY-RESEARCH ROUTING -->",
			"<!-- BEGIN missing -->",
			1,
		)),
		"duplicate block": []byte(strings.Replace(
			modelGangPromptCaptureFixture,
			"<!-- END  SECURITY-RESEARCH ROUTING -->",
			"<!-- END  SECURITY-RESEARCH ROUTING -->\n<!-- BEGIN  SECURITY-RESEARCH ROUTING -->\nduplicate\n<!-- END  SECURITY-RESEARCH ROUTING -->",
			1,
		)),
		"reversed block": []byte(strings.Replace(
			modelGangPromptCaptureFixture,
			"<!-- BEGIN  SECURITY-RESEARCH ROUTING -->",
			"<!-- END  SECURITY-RESEARCH ROUTING -->",
			1,
		)),
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := buildRemoteSkillPromptCapture(raw)
			require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
		})
	}
}

func TestRewriteRemoteSkillPublishedFilesOnlyChangesContentAcquisitionRoot(t *testing.T) {
	raw := map[string][]byte{
		"SKILL.md": []byte("REMOTE_ROOT = " + RemoteSkillMoxinggangRoot + "\n" +
			"Tool docs: https://github.com/example/tool\n"),
		"README_AI.md": []byte("Read " + RemoteSkillMoxinggangRoot + "/references/a.md\n" +
			"Project: https://github.com/example/project\n"),
		"scripts/tool.py": {0x00, 0x01, 0x02},
	}

	effective := rewriteRemoteSkillPublishedFiles(raw)
	require.Equal(t, "REMOTE_ROOT = "+RemoteSkillPublicRoot+"\nTool docs: https://github.com/example/tool\n", string(effective["SKILL.md"]))
	require.Equal(t, "Read "+RemoteSkillPublicRoot+"/references/a.md\nProject: https://github.com/example/project\n", string(effective["README_AI.md"]))
	require.True(t, bytes.Equal(raw["scripts/tool.py"], effective["scripts/tool.py"]))
	require.Equal(t, RemoteSkillMoxinggangRoot, "https://moxinggang.com/skills/security-research/current")
	require.NotSame(t, &raw, &effective)
}
