package service

import (
	"context"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestNamedPolicyCacheRechecksPersistedAdmission(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*PluginInstallation)
	}{
		{"disabled", func(i *PluginInstallation) { i.State = PluginStateDisabled; i.Bindings[0].Enabled = false }},
		{"rollout", func(i *PluginInstallation) { i.Bindings[0].RolloutPercent = 0 }},
		{"config", func(i *PluginInstallation) { i.ConfigEncrypted = "new-config" }},
		{"package", func(i *PluginInstallation) { i.PackageSHA256 = strings.Repeat("c", 64) }},
		{"operation", func(i *PluginInstallation) { i.Manifest.Operations = map[string][]string{} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			manager, repo, runtime := hostIOAdmissionManager(t)
			calls := 0
			runtime.extension = extensionv1.NewClient(&ticketExtensionTestConn{invoke: func(extensionv1.Invocation) (extensionv1.Result, error) {
				calls++
				return extensionv1.Result{Payload: []byte(`{"allowed":true}`)}, nil
			}})
			in := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "host.io", AccountID: 42, Payload: []byte(`{}`)}
			_, err := manager.InvokeCachedOperation(context.Background(), PlatformOpenAI, AccountTypeAPIKey, in)
			require.NoError(t, err)
			require.Equal(t, 1, calls, "positive control must invoke the actual serialized boundary")
			_, err = manager.InvokeCachedOperation(context.Background(), PlatformOpenAI, AccountTypeAPIKey, in)
			require.NoError(t, err)
			require.Equal(t, 1, calls, "unchanged policy can still use its cache")
			require.Equal(t, 2, repo.businessHolds, "a cache hit still acquires current business admission")
			repo.current.Revision++
			change.edit(repo.current)
			_, err = manager.InvokeCachedOperation(context.Background(), PlatformOpenAI, AccountTypeAPIKey, in)
			require.Error(t, err, "a stale local registry cannot authorize a cached policy")
			require.Equal(t, 1, calls)
			require.Zero(t, repo.active)
			require.Zero(t, runtime.inFlight.Load())
		})
	}
}

func TestNamedPolicyInvocationRejectsChangedAppliedConfigAndCanceledResult(t *testing.T) {
	for _, scenario := range []string{"unapplied", "changed-during-call", "canceled-during-call"} {
		t.Run(scenario, func(t *testing.T) {
			manager, repo, runtime := hostIOAdmissionManager(t)
			calls := 0
			runtime.extension = extensionv1.NewClient(&ticketExtensionTestConn{invoke: func(extensionv1.Invocation) (extensionv1.Result, error) {
				calls++
				if scenario == "changed-during-call" {
					runtime.configRevision.Add(1)
				} else if scenario == "canceled-during-call" {
					runtime.beginDrain()
				}
				return extensionv1.Result{Payload: []byte(`{"allowed":true}`)}, nil
			}})
			if scenario == "unapplied" {
				repo.current.ConfigEncrypted = "new-config"
				repo.current.Revision++
			}
			_, err := manager.InvokeOperation(context.Background(), PlatformOpenAI, AccountTypeAPIKey,
				extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "host.io", AccountID: 42, Payload: []byte(`{}`)})
			require.Error(t, err)
			if scenario == "unapplied" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			require.Zero(t, repo.active)
			require.Zero(t, runtime.inFlight.Load())
		})
	}
}
