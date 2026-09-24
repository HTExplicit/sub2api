package registry

import (
	"sort"
	"strings"
)

func remoteSkillMoxinggangReferences(raw []byte) []string {
	const marker = "https://moxinggang.com/skills/security-research/current/"
	seen := make(map[string]struct{})
	result := make([]string, 0)
	text := string(raw)
	for start := 0; ; {
		index := strings.Index(text[start:], marker)
		if index < 0 {
			break
		}
		index += start + len(marker)
		end := index
		for end < len(text) && !strings.ContainsRune(" \t\r\n<>()[]{}'\"`", rune(text[end])) {
			end++
		}
		name := strings.TrimRight(text[index:end], ".,;:!?")
		if normalized, err := normalizeBundleRelativePath(name); err == nil && normalized == name {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				result = append(result, name)
			}
		}
		start = end
	}
	sort.Strings(result)
	return result
}
