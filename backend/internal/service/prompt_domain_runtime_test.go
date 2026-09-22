package service

import (
	"context"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type failedPromptProcess struct{}

func TestRemoteSkillPairAssemblyKeepsCancellationDuringFilePolicyChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := buildPairedRemoteSkillCandidate(ctx, map[string][]byte{"SKILL.md": []byte("fixture")},
		map[string][]byte{"SKILL.md": []byte("fixture")}, RemoteSkillPromptCapture{}, nil, time.Now())
	require.ErrorIs(t, err, context.Canceled)
	_, err = remoteSkillFileChangesChecked(ctx, nil, RemoteSkillCandidate{EffectiveFiles: map[string][]byte{"SKILL.md": []byte("fixture")}})
	require.ErrorIs(t, err, context.Canceled)
}

func (failedPromptProcess) InvokeOperation(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error) {
	return extensionv1.Result{}, ErrExtensionOperationUnavailable
}

type boundPromptFixture struct {
	promptPolicyFixture
	runtime *pluginRuntime
}

func (f boundPromptFixture) BindOperationContext(ctx context.Context, _, _ string, _ extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	return f.runtime.bindPolicyContext(ctx)
}

type promptSourceFunc func(context.Context) (BusinessSystemPromptSourceCandidate, error)

func (f promptSourceFunc) Fetch(ctx context.Context) (BusinessSystemPromptSourceCandidate, error) {
	return f(ctx)
}

func TestPromptSourceDisableDiscardsLateCandidateBeforePersistence(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	runtime := &pluginRuntime{}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: boundPromptFixture{runtime: runtime}})
	store := &fakeBusinessSystemPromptStore{}
	prompts := NewBusinessSystemPromptService(store, nil)
	prompts.SetBusinessSystemPromptSource(promptSourceFunc(func(ctx context.Context) (BusinessSystemPromptSourceCandidate, error) {
		runtime.beginDrain()
		return BusinessSystemPromptSourceCandidate{Body: "late"}, nil
	}))
	_, err := prompts.SyncManagedSource(context.Background(), 2, 42, 1, 3)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, store.sourceSyncCalls)
}

func TestPromptDomainInitializesAfterActivationAndPreservesDataOnDisable(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	registry, registryStore, files := testRemoteSkillRegistry(t, testRemoteSkillCandidate(t, 1, 1, "seed"))
	defer registry.Stop()
	files.installed = false
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 3, Enabled: true, Body: "server"}}
	prompts := NewBusinessSystemPromptService(store, nil)
	defer prompts.Stop()
	runtime := NewPromptDomainRuntime(registry, prompts)
	processExtensionOperations.Store(nil)
	runtime.reconcile(context.Background(), time.Now())
	require.False(t, files.installed)
	require.False(t, store.seedCalled)
	processExtensionOperations.Store(previous)
	runtime.reconcile(context.Background(), time.Now())
	require.True(t, runtime.ready)
	require.True(t, files.installed)
	require.True(t, store.seedCalled)
	_, err := registry.LoadPublishedFile(context.Background(), "SKILL.md")
	require.NoError(t, err)
	processExtensionOperations.Store(nil)
	runtime.reconcile(context.Background(), time.Now())
	require.False(t, runtime.ready)
	registry.runMu.Lock()
	require.False(t, registry.started)
	registry.runMu.Unlock()
	prompts.lifecycleMu.Lock()
	require.False(t, prompts.started)
	prompts.lifecycleMu.Unlock()
	require.Equal(t, "server", prompts.snapshot.Load().Body)
	require.NotNil(t, registry.publication.Load())
	require.False(t, files.cleaned)
	require.False(t, registryStore.cleaned)
	_, err = registry.LoadPublishedFile(context.Background(), "SKILL.md")
	require.ErrorIs(t, err, ErrRemoteSkillPublicFileNotFound)
	body := []byte(`{"input":"client"}`)
	gateway := &OpenAIGatewayService{businessPromptService: prompts}
	out, applied, err := gateway.applyBusinessSystemPrompt(body, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, BusinessSystemPromptProtocolResponses, false)
	require.NoError(t, err)
	require.Equal(t, body, out)
	require.False(t, applied.Applied)
	processExtensionOperations.Store(previous)
	runtime.reconcile(context.Background(), time.Now())
	require.True(t, runtime.ready)
	registry.runMu.Lock()
	require.True(t, registry.started)
	registry.runMu.Unlock()
	prompts.lifecycleMu.Lock()
	require.True(t, prompts.started)
	prompts.lifecycleMu.Unlock()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: failedPromptProcess{}})
	_, err = registry.LoadPublishedFile(context.Background(), "SKILL.md")
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleUnavailable)
	_, _, err = gateway.applyBusinessSystemPrompt(body, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, BusinessSystemPromptProtocolResponses, false)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable, "failure of an enabled policy must not silently remove a configured prompt")
}

func TestPromptDomainHealthyStartReloadsPromptOnce(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: promptPolicyFixture{}})
	registry, _, _ := testRemoteSkillRegistry(t, testRemoteSkillCandidate(t, 1, 1, "seed"))
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Body: embeddedBusinessSystemPrompt}}
	prompts := NewBusinessSystemPromptService(store, nil)
	runtime := NewPromptDomainRuntime(registry, prompts)
	runtime.Start(context.Background())
	require.True(t, runtime.ready)
	runtime.Stop()
	require.Equal(t, 1, store.loadCalls)
	runtime.Start(context.Background())
	require.True(t, runtime.ready)
	runtime.Stop()
	require.Equal(t, 2, store.loadCalls)
}
