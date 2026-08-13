package service

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedRemoteSkillPromptMatchesApprovedNoBrandCapture(t *testing.T) {
	require.Equal(t, "74bd491260aaa23c45b82bd522b32c6b6dea7d5e76a2d8e3ab3607c6f1ab4e58", hashBusinessSystemPromptBundleBytes([]byte(embeddedBusinessSystemPrompt)))
	require.Equal(t, 120, strings.Count(embeddedBusinessSystemPrompt, "\n")+1)
	require.Equal(t, 6934, utf8.RuneCountInString(embeddedBusinessSystemPrompt))
	require.Contains(t, embeddedBusinessSystemPrompt, "You are , a friendly")
	require.Contains(t, embeddedBusinessSystemPrompt, `exactly "宝宝"`)
	require.NotContains(t, embeddedBusinessSystemPrompt, "CodexRip")
	require.NotContains(t, embeddedBusinessSystemPrompt, "模型港")

	capture, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	require.NoError(t, err)
	require.Equal(t, "53611ff9c5213a32f6441431296e55a6538dfe661f223d645cd6f483c941b610", capture.EffectiveSHA256)
	rawFirst, err := locateUniqueRemoteSkillPromptBlock(capture.RawBody, remoteSkillRoutingBegin, remoteSkillRoutingEnd)
	require.NoError(t, err)
	rawSecond, err := locateUniqueRemoteSkillPromptBlock(capture.RawBody, remoteSkillSecurityResearchRoutingBegin, remoteSkillSecurityResearchRoutingEnd)
	require.NoError(t, err)
	effectiveFirst, err := locateUniqueRemoteSkillPromptBlock(capture.EffectiveBody, remoteSkillRoutingBegin, remoteSkillRoutingEnd)
	require.NoError(t, err)
	effectiveSecond, err := locateUniqueRemoteSkillPromptBlock(capture.EffectiveBody, remoteSkillSecurityResearchRoutingBegin, remoteSkillSecurityResearchRoutingEnd)
	require.NoError(t, err)
	require.Equal(t, capture.RawBody[rawFirst.end:rawSecond.begin], capture.EffectiveBody[effectiveFirst.end:effectiveSecond.begin])
	require.Equal(t, capture.RawBody[rawSecond.end:], capture.EffectiveBody[effectiveSecond.end:])
}

const modelGangPromptCaptureFixture = `<!-- BEGIN  REVERSE-SKILL -->
#  Codex reverse-skill routing
For reverse engineering, security research, and CTF tasks, read these files first:
- C:\Users\Administrator\AppData\Local\\reverse-skill\RULES.md
- C:\Users\Administrator\AppData\Local\\reverse-skill\README_AI.md
- C:\Users\Administrator\AppData\Local\\reverse-skill\skills\SKILL.md
<!-- END  REVERSE-SKILL -->
You are , a friendly and highly capable senior technical-engineering assistant.
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

func TestBuildRemoteSkillPromptCaptureRewritesOnlyTwoUniqueRoutingBlocks(t *testing.T) {
	capture, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)

	want := remoteSkillRoutingBlock + "\n" +
		"You are , a friendly and highly capable senior technical-engineering assistant.\n" +
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

func TestRemoteSkillEligibilityGatePerformsNoEntryIOAndExcludesOrdinaryWork(t *testing.T) {
	capture, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	require.NoError(t, err)
	block, err := locateUniqueRemoteSkillPromptBlock(
		capture.EffectiveBody,
		remoteSkillRoutingBegin,
		remoteSkillRoutingEnd,
	)
	require.NoError(t, err)
	gate := string(capture.EffectiveBody[block.begin:block.end])
	lowerGate := strings.ToLower(gate)

	require.Contains(t, gate, "zero I/O")
	require.Contains(t, lowerGate, "eligibility decision only")
	require.Contains(t, lowerGate, "ordinary performance")
	require.Contains(t, lowerGate, "network-path or api integration")
	require.Contains(t, lowerGate, "service operations")
	require.Contains(t, lowerGate, "frontend, ui, product")
	require.Contains(t, lowerGate, "maintenance, debugging, or explanation of this skill")
	require.NotContains(t, gate, RemoteSkillPublicRoot)
	require.NotContains(t, gate, "RULES.md")
	require.NotContains(t, gate, "README_AI.md")
	require.NotContains(t, gate, "SKILL.md")
	require.NotContains(t, gate, "REMOTE_ROOT")
	require.NotContains(t, gate, "HTTP GET")
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
	require.Contains(t, lowerLoader, "hosted web search")
	require.Contains(t, lowerLoader, "non-empty body")
	require.Contains(t, lowerLoader, "valid utf-8")
	require.Contains(t, lowerLoader, "at most once with a different raw http client")
	require.Contains(t, lowerLoader, "do not restart the loading pass")
	require.Contains(t, lowerLoader, "do not refetch files that already succeeded")
	require.Contains(t, lowerLoader, "at most one entry-loading pass")
	require.Contains(t, lowerLoader, "follow-up turns in the same task must reuse")
	require.Contains(t, lowerLoader, "local, installed, bundled, or same-name skill")
	require.Contains(t, lowerLoader, "another origin")
	require.NotContains(t, loader, RemoteSkillMoxinggangRoot)
	require.NotContains(t, lowerLoader, "raw.githubusercontent.com")
}

func TestBuildRemoteSkillPromptCaptureRejectsMalformedCaptures(t *testing.T) {
	tests := map[string][]byte{
		"invalid UTF-8":       {0xff},
		"missing first block": []byte(strings.Replace(modelGangPromptCaptureFixture, "<!-- BEGIN  REVERSE-SKILL -->", "<!-- BEGIN missing -->", 1)),
		"duplicate first block": []byte(strings.Replace(
			modelGangPromptCaptureFixture,
			"<!-- END  REVERSE-SKILL -->",
			"<!-- END  REVERSE-SKILL -->\n<!-- BEGIN  REVERSE-SKILL -->\nduplicate\n<!-- END  REVERSE-SKILL -->",
			1,
		)),
		"reversed second block": []byte(strings.Replace(
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
