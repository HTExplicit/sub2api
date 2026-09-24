package service

import (
	"context"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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

func TestPromptDomainHealthyStartReloadsPromptOnce(t *testing.T) {
	previous := invokePromptSkills
	t.Cleanup(func() { invokePromptSkills = previous })
	invokePromptSkills = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		return (promptPolicyFixture{}).InvokeOperation(ctx, "", "", in)
	}
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
