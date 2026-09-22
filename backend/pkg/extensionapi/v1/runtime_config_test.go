package extensionv1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type staticConfigModule struct {
	validations, statuses, applies, invocations int
	validationErr, statusErr                    error
}

func (m *staticConfigModule) ValidateConfig(context.Context, json.RawMessage) (json.RawMessage, error) {
	m.validations++
	return json.RawMessage(`{"enabled":true}`), m.validationErr
}
func (m *staticConfigModule) ApplyConfig(context.Context, json.RawMessage) error {
	m.applies++
	return nil
}
func (m *staticConfigModule) Invoke(context.Context, Invocation) (Result, error) {
	m.invocations++
	return Result{}, errors.New("business operation must not run")
}
func (m *staticConfigModule) Status(context.Context) (json.RawMessage, error) {
	m.statuses++
	return json.RawMessage(`{"ready":true}`), m.statusErr
}

func TestRuntimeConfigTestReportsOnlyLocalValidation(t *testing.T) {
	module := &staticConfigModule{}
	runtime := NewRuntime(Definition{ID: "fixture.config", Version: "1.0.0"}, module)
	result, err := runtime.TestConfig(context.Background(), &pluginv1.TestConfigRequest{ConfigJson: []byte(`{}`)})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("valid independent configuration did not get a diagnostic result: %v, %v", result, err)
	}
	if result.StatusJson != `{"ready":true}` || result.Message != "Configuration and local status are valid; no upstream connection was tested." {
		t.Fatalf("diagnostic scope is not explicit: %+v", result)
	}
	if module.validations != 1 || module.statuses != 1 || module.applies != 0 || module.invocations != 0 {
		t.Fatalf("configuration diagnostic performed unexpected work: %+v", module)
	}
}

func TestRuntimeConfigTestRejectsInvalidOrCanceledWithoutBusinessWork(t *testing.T) {
	for _, scenario := range []string{"missing", "oversized", "canceled", "invalid-config", "failed-status"} {
		t.Run(scenario, func(t *testing.T) {
			module := &staticConfigModule{}
			request := &pluginv1.TestConfigRequest{ConfigJson: []byte(`{}`)}
			ctx := context.Background()
			switch scenario {
			case "missing":
				request = nil
			case "oversized":
				request.ConfigJson = make([]byte, MaxPayloadBytes+1)
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "invalid-config":
				module.validationErr = errors.New("synthetic-private-config-detail")
			case "failed-status":
				module.statusErr = errors.New("synthetic-private-status-detail")
			}
			result, err := NewRuntime(Definition{}, module).TestConfig(ctx, request)
			if scenario == "invalid-config" || scenario == "failed-status" {
				if err != nil || result == nil || result.Success || result.StatusJson != "" {
					t.Fatalf("failed local check must not report success: %+v, %v", result, err)
				}
			} else {
				want := codes.InvalidArgument
				if scenario == "canceled" {
					want = codes.Canceled
				}
				if status.Code(err) != want || module.validations != 0 || module.statuses != 0 {
					t.Fatalf("invalid admission should stop before module calls: %+v, %v", module, err)
				}
			}
			if module.applies != 0 || module.invocations != 0 {
				t.Fatal("configuration check performed a business action")
			}
		})
	}
}
