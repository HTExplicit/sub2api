package service

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const (
	RemoteSkillPublicRoot = "https://codexrip.vip/skills/security-research/current"

	// Legacy markers are retained only for validating already-published dual-marker pairs.
	remoteSkillRoutingBegin                 = "<!-- BEGIN  REVERSE-SKILL -->"
	remoteSkillRoutingEnd                   = "<!-- END  REVERSE-SKILL -->"
	remoteSkillSecurityResearchRoutingBegin = "<!-- BEGIN  SECURITY-RESEARCH ROUTING -->"
	remoteSkillSecurityResearchRoutingEnd   = "<!-- END  SECURITY-RESEARCH ROUTING -->"
)

type RemoteSkillPromptCapture struct {
	RawBody         []byte
	EffectiveBody   []byte
	RawSHA256       string
	EffectiveSHA256 string
	Diff            string
}

type remoteSkillPromptBlock struct {
	begin int
	end   int
}

func buildRemoteSkillPromptCapture(raw []byte) (RemoteSkillPromptCapture, error) {
	if _, _, err := ValidateBusinessSystemPromptBody(string(raw)); err != nil {
		return RemoteSkillPromptCapture{}, err
	}
	effective, err := rewriteRemoteSkillPromptRoot(raw)
	if err != nil {
		return RemoteSkillPromptCapture{}, err
	}
	if _, _, err := ValidateBusinessSystemPromptBody(string(effective)); err != nil {
		return RemoteSkillPromptCapture{}, err
	}

	diff, err := remoteSkillPromptUnifiedDiff(raw, effective)
	if err != nil {
		return RemoteSkillPromptCapture{}, err
	}
	return RemoteSkillPromptCapture{
		RawBody:         bytes.Clone(raw),
		EffectiveBody:   effective,
		RawSHA256:       hashBusinessSystemPromptBundleBytes(raw),
		EffectiveSHA256: hashBusinessSystemPromptBundleBytes(effective),
		Diff:            diff,
	}, nil
}

func rewriteRemoteSkillPromptRoot(raw []byte) ([]byte, error) {
	block, err := locateUniqueRemoteSkillPromptBlock(raw, remoteSkillSecurityResearchRoutingBegin, remoteSkillSecurityResearchRoutingEnd)
	if err != nil {
		return nil, err
	}

	upstreamRoot := []byte(RemoteSkillMoxinggangRoot)
	if bytes.Count(raw, upstreamRoot) != 1 {
		return nil, fmt.Errorf("%w: upstream Skill root must appear exactly once", ErrBusinessSystemPromptInvalid)
	}
	rootStart := bytes.Index(raw, upstreamRoot)
	if rootStart < block.begin || rootStart+len(upstreamRoot) > block.end {
		return nil, fmt.Errorf("%w: upstream Skill root must be inside the routing block", ErrBusinessSystemPromptInvalid)
	}
	return bytes.Replace(raw, upstreamRoot, []byte(RemoteSkillPublicRoot), 1), nil
}

// rewriteRemoteSkillPromptBlocks supports self-consistency validation fixtures
// for historical dual-marker pairs. New candidates use rewriteRemoteSkillPromptBlock.
func rewriteRemoteSkillPromptBlocks(raw []byte, routingBlock, securityResearchRoutingBlock string) ([]byte, error) {
	first, err := locateUniqueRemoteSkillPromptBlock(raw, remoteSkillRoutingBegin, remoteSkillRoutingEnd)
	if err != nil {
		return nil, err
	}
	second, err := locateUniqueRemoteSkillPromptBlock(raw, remoteSkillSecurityResearchRoutingBegin, remoteSkillSecurityResearchRoutingEnd)
	if err != nil {
		return nil, err
	}
	if first.end > second.begin {
		return nil, fmt.Errorf("%w: prompt routing blocks overlap or are out of order", ErrBusinessSystemPromptInvalid)
	}

	effective := make([]byte, 0, len(raw)+len(routingBlock)+len(securityResearchRoutingBlock))
	effective = append(effective, raw[:first.begin]...)
	effective = append(effective, routingBlock...)
	effective = append(effective, raw[first.end:second.begin]...)
	effective = append(effective, securityResearchRoutingBlock...)
	effective = append(effective, raw[second.end:]...)
	return effective, nil
}

func remoteSkillPromptUnifiedDiff(raw, effective []byte) (string, error) {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(raw)),
		B:        difflib.SplitLines(string(effective)),
		FromFile: "prompt_capture",
		ToFile:   "effective_prompt",
		Context:  3,
	})
	if err != nil {
		return "", fmt.Errorf("%w: prompt diff failed", ErrBusinessSystemPromptInvalid)
	}
	return diff, nil
}

func locateUniqueRemoteSkillPromptBlock(raw []byte, beginMarker, endMarker string) (remoteSkillPromptBlock, error) {
	beginBytes := []byte(beginMarker)
	endBytes := []byte(endMarker)
	if bytes.Count(raw, beginBytes) != 1 || bytes.Count(raw, endBytes) != 1 {
		return remoteSkillPromptBlock{}, fmt.Errorf("%w: prompt routing marker must appear exactly once: %s", ErrBusinessSystemPromptInvalid, beginMarker)
	}
	begin := bytes.Index(raw, beginBytes)
	endStart := bytes.Index(raw, endBytes)
	if begin < 0 || endStart < begin+len(beginBytes) {
		return remoteSkillPromptBlock{}, fmt.Errorf("%w: prompt routing block is malformed: %s", ErrBusinessSystemPromptInvalid, beginMarker)
	}
	return remoteSkillPromptBlock{begin: begin, end: endStart + len(endBytes)}, nil
}

func rewriteRemoteSkillPublishedFiles(raw map[string][]byte) map[string][]byte {
	effective := make(map[string][]byte, len(raw))
	for name, data := range raw {
		cloned := bytes.Clone(data)
		if remoteSkillFileKind(name, data) == "text" {
			cloned = []byte(strings.ReplaceAll(string(data), RemoteSkillMoxinggangRoot, RemoteSkillPublicRoot))
		}
		effective[name] = cloned
	}
	return effective
}
