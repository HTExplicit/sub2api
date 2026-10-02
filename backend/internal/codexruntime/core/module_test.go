package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestValidateConfigDropsRetiredSettings(t *testing.T) {
	module := NewModule()
	for raw, want := range map[string]string{
		// Every setting a stored configuration of the retired route feature holds.
		`{"routing_schema":2,"enabled":true,"fail_closed":true,"proxy_url":"http://user:secret@proxy.test:8080","proxy_protocol":"https","proxy_selection_id":"selection","models":["gpt-6-astra","gpt-6-sol"],"request_zstd":false}`: `{"request_zstd":false}`,
		// Retired settings are never interpreted, whatever their value.
		`{"enabled":null,"models":null,"proxy_url":null,"routing_schema":"x","fail_closed":"yes"}`: `{"request_zstd":true}`,
		`{}`: `{"request_zstd":true}`,
	} {
		normalized, err := module.ValidateConfig(context.Background(), json.RawMessage(raw))
		if err != nil || string(normalized) != want {
			t.Fatalf("ValidateConfig(%s) = %s, %v; want %s", raw, normalized, err, want)
		}
	}
	for raw, want := range map[string]string{
		`null`:                               "expected a JSON object",
		`{} {}`:                              "invalid codex runtime configuration",
		`{"enabled":true,"extra":true}`:      "unknown codex runtime setting: extra",
		`{"request_zstd":null}`:              "null codex runtime setting: request_zstd",
		`{"request_zstd":"yes"}`:             "invalid codex runtime configuration",
		`{"Request_Zstd":false,"models":[]}`: "unknown codex runtime setting: Request_Zstd",
	} {
		if _, err := module.ValidateConfig(context.Background(), json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("ValidateConfig(%s) error %v, want %q", raw, err, want)
		}
	}
}

func TestModuleServesTransportPlanFromItsConfiguration(t *testing.T) {
	module := NewModule()
	ctx := context.Background()
	plan := func() (extensionv1.CodexTransportPlan, error) {
		result, err := module.Invoke(ctx, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.transport.plan",
			Payload: []byte(`{"method":"POST","path":"/backend-api/codex/responses","body_present":true}`)})
		var value extensionv1.CodexTransportPlan
		if err == nil {
			err = json.Unmarshal(result.Payload, &value)
		}
		return value, err
	}
	if err := module.ApplyConfig(ctx, json.RawMessage(`{"enabled":true,"request_zstd":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := module.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if value, err := plan(); err != nil || value.Compress {
		t.Fatalf("plan with request_zstd=false: %+v, %v", value, err)
	}
	for _, raw := range []string{`null`, `{"extra":true}`, `{"request_zstd":null}`} {
		if err := module.ApplyConfig(ctx, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid configuration %s", raw)
		}
	}
	if value, err := plan(); err != nil || value.Compress {
		t.Fatalf("an invalid configuration changed the plan: %+v, %v", value, err)
	}
	if err := module.ApplyConfig(ctx, json.RawMessage(`{"request_zstd":true}`)); err != nil {
		t.Fatal(err)
	}
	if value, err := plan(); err != nil || !value.Compress || !value.Enabled {
		t.Fatalf("plan with request_zstd=true: %+v, %v", value, err)
	}
	if _, err := module.Invoke(ctx, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "inject"}); err == nil {
		t.Fatal("a retired route operation was served")
	}
	if err := module.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := plan(); !errors.Is(err, ErrStopping) {
		t.Fatalf("plan after Stop: %v", err)
	}
	identity, err := module.Invoke(ctx, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.available", Payload: []byte(`{}`)})
	if err != nil || !strings.Contains(string(identity.Payload), `"valid":true`) {
		t.Fatalf("identity policy after Stop: %s, %v", identity.Payload, err)
	}
}
