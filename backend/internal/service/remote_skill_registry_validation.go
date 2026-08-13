package service

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	remoteSkillMarkdownLinkPattern = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	remoteSkillInlineCodePattern   = regexp.MustCompile("`[^`]*`")
	remoteSkillExternalURLPattern  = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	remoteSkillWindowsPathPattern  = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	remoteSkillLiteralRoutePattern = regexp.MustCompile("`((?:references|skills|scripts|assets)/[^`\\s]+)`")
)

var remoteSkillRequiredPaths = []string{
	"SKILL.md",
	"LICENSE",
	"NOTICE.md",
	"CHANGELOG.md",
	"agents/openai.yaml",
	"references/routing.md",
	"references/evidence-workflow.md",
	"references/scope-and-evidence.md",
	"references/reporting.md",
	"references/experience-index.md",
	"scripts/env_probe.py",
	"scripts/reusable/artifact_inventory.py",
	"scripts/reusable/route_task.py",
	"scripts/validate_result.py",
	"schemas/research-result.schema.json",
	"assets/templates/ctf-writeup.md",
	"assets/templates/research-result.json",
}

var remoteSkillRequiredModules = []string{
	"sec-web-api",
	"sec-pwn-native",
	"sec-reverse",
	"sec-crypto",
	"sec-forensics-dfir",
	"sec-malware",
	"sec-misc",
	"sec-osint",
	"sec-ai-security",
	"sec-assessment-tooling",
	"sec-reporting",
}

func validateCurrentRemoteSkillTree(files map[string][]byte) error {
	manifest, err := loadRemoteSkillManifest()
	if err != nil {
		return err
	}
	if err := validateGenericRemoteSkillTree(files); err != nil {
		return err
	}
	if len(files) != len(manifest.Files) {
		return fmt.Errorf("%w: current tree file count mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	approved := make(map[string]struct{}, len(manifest.Files))
	for _, entry := range manifest.Files {
		body, ok := files[entry.Path]
		if !ok || !remoteSkillManifestEntryMatches(entry, body) {
			return fmt.Errorf("%w: current tree does not match manifest", ErrBusinessSystemPromptBundleInvalid)
		}
		approved[entry.Path] = struct{}{}
	}
	for name := range files {
		if _, ok := approved[name]; !ok {
			return fmt.Errorf("%w: current tree contains undeclared file", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	for _, name := range remoteSkillRequiredPaths {
		if _, ok := files[name]; !ok {
			return fmt.Errorf("%w: required remote skill file missing", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	for _, module := range remoteSkillRequiredModules {
		if _, ok := files[path.Join("skills", module, "INSTRUCTIONS.md")]; !ok {
			return fmt.Errorf("%w: required remote skill module missing", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	for name, body := range files {
		if strings.EqualFold(path.Ext(name), ".md") {
			if err := validateRemoteSkillMarkdownClosure(name, string(body), files); err != nil {
				return err
			}
		}
		for _, reference := range remoteSkillMoxinggangReferences(body) {
			if _, ok := approved[reference]; !ok {
				return fmt.Errorf("%w: upstream introduced an unapproved reference", ErrBusinessSystemPromptBundleInvalid)
			}
		}
	}
	return nil
}

func validateGenericRemoteSkillTree(files map[string][]byte) error {
	if len(files) == 0 || len(files) > remoteSkillMaxFileCount {
		return fmt.Errorf("%w: paired tree file count invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	portable := make(map[string]string, len(files))
	var total int64
	for name, body := range files {
		normalized, err := normalizeBundleRelativePath(name)
		if err != nil || normalized != name || !norm.NFC.IsNormalString(name) || len(body) == 0 ||
			len(body) > businessSystemPromptBundleMaxFileBytes || !utf8.Valid(body) {
			return fmt.Errorf("%w: paired tree path or body invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		key := portableRemoteSkillPathKey(name)
		if previous, exists := portable[key]; exists && previous != name {
			return fmt.Errorf("%w: portable path collision", ErrBusinessSystemPromptBundleInvalid)
		}
		portable[key] = name
		total += int64(len(body))
		if total > remoteSkillMaxTotalBytes {
			return fmt.Errorf("%w: paired tree exceeds size limit", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	for _, core := range []string{"RULES.md", "README_AI.md", "SKILL.md"} {
		if _, ok := files[core]; !ok {
			return fmt.Errorf("%w: upstream entry file missing", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	return nil
}

func validateRemoteSkillMarkdownClosure(name, content string, files map[string][]byte) error {
	fence := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		marker := ""
		if len(trimmed) >= 3 && (trimmed[:3] == "```" || trimmed[:3] == "~~~") {
			marker = trimmed[:3]
		}
		if marker != "" {
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		plain := remoteSkillInlineCodePattern.ReplaceAllString(line, "")
		for _, match := range remoteSkillMarkdownLinkPattern.FindAllStringSubmatchIndex(plain, -1) {
			raw := strings.TrimSpace(plain[match[2]:match[3]])
			if strings.HasPrefix(raw, "<") {
				if end := strings.IndexByte(raw, '>'); end >= 0 {
					raw = raw[1:end]
				}
			} else if index := remoteSkillMarkdownTitleIndex(raw); index >= 0 {
				raw = raw[:index]
			}
			if raw == "" || strings.HasPrefix(raw, "#") || remoteSkillExternalURLPattern.MatchString(raw) {
				continue
			}
			target := strings.SplitN(raw, "#", 2)[0]
			if target == "" {
				continue
			}
			resolved, err := resolveRemoteSkillRelativeTarget(name, target)
			if err != nil || !remoteSkillTargetExists(files, resolved) {
				return fmt.Errorf("%w: broken relative Markdown link in %s", ErrBusinessSystemPromptBundleInvalid, name)
			}
		}
		if remoteSkillLiteralRouteFile(name) {
			for _, match := range remoteSkillLiteralRoutePattern.FindAllStringSubmatch(line, -1) {
				target := strings.TrimRight(match[1], ".,;:")
				if strings.ContainsAny(target, "*{}") {
					continue
				}
				if !remoteSkillLiteralTargetExists(files, name, target) {
					return fmt.Errorf("%w: broken literal route in %s", ErrBusinessSystemPromptBundleInvalid, name)
				}
			}
		}
	}
	return nil
}

func remoteSkillMarkdownTitleIndex(raw string) int {
	for index := 1; index < len(raw); index++ {
		if (raw[index] == '"' || raw[index] == '\'') && (raw[index-1] == ' ' || raw[index-1] == '\t') {
			start := index - 1
			for start > 0 && (raw[start-1] == ' ' || raw[start-1] == '\t') {
				start--
			}
			return start
		}
	}
	return -1
}

func resolveRemoteSkillRelativeTarget(baseFile, target string) (string, error) {
	if remoteSkillWindowsPathPattern.MatchString(target) || strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\\`) || strings.Contains(target, `\`) {
		return "", fmt.Errorf("absolute or non-portable target")
	}
	resolved := path.Clean(path.Join(path.Dir(baseFile), target))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("target escapes tree")
	}
	if normalized, err := normalizeBundleRelativePath(resolved); err != nil || normalized != resolved {
		return "", fmt.Errorf("target is not canonical")
	}
	return resolved, nil
}

func remoteSkillTargetExists(files map[string][]byte, target string) bool {
	if _, ok := files[target]; ok {
		return true
	}
	prefix := strings.TrimSuffix(target, "/") + "/"
	for name := range files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func remoteSkillLiteralRouteFile(name string) bool {
	base := path.Base(name)
	return base == "SKILL.md" || base == "INSTRUCTIONS.md" || base == "experience-index.md" || base == "routing.md"
}

func remoteSkillLiteralTargetExists(files map[string][]byte, name, target string) bool {
	bases := []string{""}
	parts := strings.Split(name, "/")
	if remoteSkillPathHasPart(parts, "ctf-orchestrator") || remoteSkillPathHasPart(parts, "pentest-tools") {
		bases = append(bases, path.Dir(name))
	} else if len(parts) >= 2 && parts[0] == "skills" {
		bases = append(bases, path.Join(parts[0], parts[1]))
	}
	for _, base := range bases {
		candidate := path.Clean(path.Join(base, target))
		if candidate != "." && candidate != ".." && !strings.HasPrefix(candidate, "../") && remoteSkillTargetExists(files, candidate) {
			return true
		}
	}
	return false
}

func remoteSkillPathHasPart(parts []string, expected string) bool {
	for _, part := range parts {
		if part == expected {
			return true
		}
	}
	return false
}

func portableRemoteSkillPathKey(name string) string {
	return cases.Fold().String(norm.NFC.String(strings.ReplaceAll(name, `\`, "/")))
}
