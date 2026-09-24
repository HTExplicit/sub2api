package tickets

import (
	"net/http"
	"strconv"
	"strings"
)

const astraMinimumClientVersion = "0.153.4"

// Keep the established Astra acquisition identity floor in the domain plugin.
// A newer host-configured identity is preserved verbatim.
func applyHarvestIdentity(headers http.Header, model string) {
	model = strings.ToLower(strings.TrimSpace(model))
	if !strings.Contains(model, "gpt-6") && !strings.Contains(model, "astra") {
		return
	}
	if !versionBefore(headers.Get("version"), astraMinimumClientVersion) {
		return
	}
	headers.Set("version", astraMinimumClientVersion)
	headers.Set("originator", "codex-tui")
	headers.Set("user-agent", "codex-tui/"+astraMinimumClientVersion+" (Ubuntu 22.4.0; x86_64) xterm-256color")
}

func versionBefore(actual, minimum string) bool {
	version, pre, _ := strings.Cut(strings.TrimSpace(actual), "-")
	left, right := strings.Split(version, "."), strings.Split(minimum, ".")
	for i := 0; i < len(right); i++ {
		if i >= len(left) {
			return true
		}
		a, err := strconv.Atoi(left[i])
		if err != nil {
			return true
		}
		b, _ := strconv.Atoi(right[i])
		if a != b {
			return a < b
		}
	}
	return pre != ""
}
