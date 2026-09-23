package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGPT6NamedModelIdentity(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"gpt-6-sol", "gpt-6-sol"}, {"gpt-6-luna", "gpt-6-luna"},
		{"OPENAI/GPT_6_SOL-max", "gpt-6-sol"}, {"gpt-6-luna-none", "gpt-6-luna"},
		{"gpt-6-sol-openai-compact", "gpt-6-sol"},
		{"gpt-6", ""}, {"gpt-6-astra", ""}, {"gpt-6-terra", ""},
		{"gpt-6-solstice", ""}, {"gpt-6-sol-2099-01-01", ""}, {"gpt-6-luna-ultra", ""},
	} {
		if got := GPT6NamedModel(tc.model); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.model, got, tc.want)
		}
	}
}

func TestGPT6InstructionsMatchPinnedCatalog(t *testing.T) {
	body, err := os.ReadFile("gpt6_codex_reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Commit string `json:"source_commit"`
		Models map[string]struct {
			SHA     string `json:"instructions_sha256"`
			Context int64  `json:"context_window"`
			Maximum int64  `json:"max_context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Commit != GPT6CodexReferenceCommit {
		t.Fatal("instruction metadata must use the pinned official commit")
	}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		entry, ok := reference.Models[model]
		if !ok || entry.Context != GPT6CodexContextWindow || entry.Maximum != GPT6CodexMaxContextWindow {
			t.Fatalf("%s: missing or mismatched Codex reference", model)
		}
		instructions := CodexBaseInstructionsForModel(model)
		digest := sha256.Sum256([]byte(instructions))
		if hex.EncodeToString(digest[:]) != entry.SHA || !strings.HasPrefix(instructions, "You are Codex") {
			t.Errorf("%s: instructions differ from the extracted official template", model)
		}
		if CodexBaseInstructionsForModel("openai/"+model+"-max") != instructions {
			t.Errorf("%s: effort alias selected a different model's instructions", model)
		}
		if instructions == CodexBaseInstructionsForModel("gpt-6-astra") {
			t.Errorf("%s: inherited Astra instructions", model)
		}
	}
	if CodexBaseInstructionsForModel("gpt-6-unknown") == CodexBaseInstructionsForModel("gpt-6-astra") {
		t.Fatal("unknown GPT-6 family must not inherit Astra instructions")
	}
}
