package tickets

import (
	"net/http"
	"testing"
)

func TestHarvestIdentityKeepsAstraFloorAndNewerOperatorIdentity(t *testing.T) {
	for _, version := range []string{"", "0.146.0", "0.153.3", "0.153.4-alpha.1"} {
		headers := http.Header{"Version": []string{version}, "User-Agent": []string{"old"}}
		applyHarvestIdentity(headers, "gpt-6-astra")
		if headers.Get("version") != astraMinimumClientVersion || headers.Get("originator") != "codex-tui" {
			t.Fatalf("Astra floor missing for %q", version)
		}
	}
	headers := http.Header{"Version": []string{"0.200.1"}, "User-Agent": []string{"configured-agent"}}
	applyHarvestIdentity(headers, "gpt-6-astra")
	if headers.Get("user-agent") != "configured-agent" {
		t.Fatal("newer configured identity was overwritten")
	}
	headers.Set("version", "0.146.0")
	applyHarvestIdentity(headers, "gpt-5.6-sol")
	if headers.Get("version") != "0.146.0" {
		t.Fatal("unrelated model acquired Astra-only identity policy")
	}
}
