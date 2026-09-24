package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cindy "github.com/Wei-Shaw/sub2api/internal/cindyprovider/catalog"
	codexprofile "github.com/Wei-Shaw/sub2api/internal/codexruntime/profile"
	codexrecovery "github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"

	accounttools "github.com/Wei-Shaw/sub2api/internal/accounttools/policy"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	policy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"
)

type promptPolicyFixture struct{}

type codexTransportFixtureContextKey struct{}

func withCodexTransportFixture(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, codexTransportFixtureContextKey{}, enabled)
}

var promptFixtureModule = policy.New()

func cindyProbeTestModels(t *testing.T) [2]string {
	t.Helper()
	plan, err := cindyBalanceProbePlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan.Models
}

func (promptPolicyFixture) InvokeOperation(ctx context.Context, _ string, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Capability == extensionv1.CapabilityRecovery {
		return codexrecovery.Invoke(ctx, in)
	}
	if in.Operation == "codex.transport.plan" {
		var query extensionv1.CodexTransportQuery
		if err := json.Unmarshal(in.Payload, &query); err != nil {
			return extensionv1.Result{}, err
		}
		enabled, _ := ctx.Value(codexTransportFixtureContextKey{}).(bool)
		raw, err := json.Marshal(codexprofile.TransportPlan(query, enabled))
		return extensionv1.Result{Payload: raw}, err
	}
	if strings.HasPrefix(in.Operation, "codex.identity.") {
		return codexprofile.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "cindy.") {
		module := cindy.New()
		raw, _ := json.Marshal(LegacyCindyProviderConfig())
		if err := module.ApplyConfig(ctx, raw); err != nil {
			return extensionv1.Result{}, err
		}
		return module.Invoke(ctx, in)
	}
	if strings.HasPrefix(in.Operation, "taxonomy.") || strings.HasPrefix(in.Operation, "test.") || strings.HasPrefix(in.Operation, "import.") || in.Operation == "tools.describe" {
		return accounttools.New().Invoke(ctx, in)
	}
	return promptFixtureModule.Invoke(ctx, in)
}
func init() {
	invokeNativeCodex = promptPolicyFixture{}.InvokeOperation
	bindNativeCodexContext = func(ctx context.Context, _ string, _ string, _ extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
		bound, cancel := context.WithCancel(ctx)
		return bound, cancel, nil
	}
}
