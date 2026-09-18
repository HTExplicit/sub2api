package service

import "testing"

func TestNormalizeUpstreamBaseURLInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "host shorthand", in: " relay.example.com/v1 ", want: "https://relay.example.com/v1"},
		{name: "scheme preserved", in: "https://relay.example.com/v1", want: "https://relay.example.com/v1"},
		{name: "http scheme preserved", in: "http://relay.example.com", want: "http://relay.example.com"},
		{name: "protocol relative", in: "//relay.example.com/v1", want: "https://relay.example.com/v1"},
		{name: "empty", in: "  ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeUpstreamBaseURLInput(tt.in); got != tt.want {
				t.Fatalf("NormalizeUpstreamBaseURLInput(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeAccountCredentialBaseURLs(t *testing.T) {
	credentials := map[string]any{
		"base_url": "relay.example.com",
		"api_base_urls": map[string]any{
			"chat_completions": "relay.example.com/v1",
			"anthropic":        "https://relay.example.com/anthropic",
		},
	}
	NormalizeAccountCredentialBaseURLs(credentials)
	if got := credentials["base_url"]; got != "https://relay.example.com" {
		t.Fatalf("base_url = %#v", got)
	}
	urls, ok := credentials["api_base_urls"].(map[string]any)
	if !ok {
		t.Fatalf("api_base_urls has unexpected type %T", credentials["api_base_urls"])
	}
	if got := urls["chat_completions"]; got != "https://relay.example.com/v1" {
		t.Fatalf("chat_completions = %#v", got)
	}
	if got := urls["anthropic"]; got != "https://relay.example.com/anthropic" {
		t.Fatalf("anthropic = %#v", got)
	}
}
