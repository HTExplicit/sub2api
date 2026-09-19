package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
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

type RemoteSkillPromptCapture = extensionv1.SkillPromptCapture

type remoteSkillPromptBlock struct {
	begin int
	end   int
}

func buildRemoteSkillPromptCapture(raw []byte) (RemoteSkillPromptCapture, error) {
	var capture RemoteSkillPromptCapture
	err := invokePromptManagement(context.Background(), "skills.capture", map[string]any{"body": raw}, &capture)
	if err != nil {
		return capture, err
	}
	if !bytes.Equal(raw, capture.RawBody) || hashBusinessSystemPromptBundleBytes(raw) != capture.RawSHA256 || hashBusinessSystemPromptBundleBytes(capture.EffectiveBody) != capture.EffectiveSHA256 {
		return RemoteSkillPromptCapture{}, fmt.Errorf("%w: inconsistent prompt capture", ErrBusinessSystemPromptInvalid)
	}
	return capture, nil
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

func rewriteRemoteSkillPublishedFilesChecked(ctx context.Context, raw map[string][]byte) (map[string][]byte, error) {
	effective := make(map[string][]byte, len(raw))
	var total int64
	for name, data := range raw {
		plan, err := remoteSkillFilePlan(ctx, name, data)
		if err != nil {
			return nil, err
		}
		expected := int64(len(data))
		if plan.ReplaceFrom != "" {
			expected += int64(bytes.Count(data, []byte(plan.ReplaceFrom))) * int64(len(plan.ReplaceTo)-len(plan.ReplaceFrom))
		}
		total += expected
		if expected <= 0 || expected > businessSystemPromptBundleMaxFileBytes || total > remoteSkillMaxTotalBytes {
			return nil, ErrBusinessSystemPromptBundleInvalid
		}
		cloned := bytes.Clone(data)
		if plan.ReplaceFrom != "" {
			cloned = []byte(strings.ReplaceAll(string(data), plan.ReplaceFrom, plan.ReplaceTo))
		}
		effective[name] = cloned
	}
	return effective, nil
}
