package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestSkillProfileCannotGrantANewNetworkOrPublicationOrigin(t *testing.T) {
	profile := extensionv1.SkillRegistryPolicyProfile{SourceID: RemoteSkillUpstreamSourceID, UpstreamRoot: RemoteSkillUpstreamRoot, PublicRoot: RemoteSkillPublicRoot, MaxFileCount: 2000, MaxTotalBytes: 256 << 20, StorageLayoutVersion: 1}
	require.NoError(t, validateRemoteSkillRegistryProfile(profile))
	for _, mutate := range []func(*extensionv1.SkillRegistryPolicyProfile){
		func(p *extensionv1.SkillRegistryPolicyProfile) { p.UpstreamRoot = "https://127.0.0.1/private" },
		func(p *extensionv1.SkillRegistryPolicyProfile) { p.UpstreamRoot = "https://example.invalid/other" },
		func(p *extensionv1.SkillRegistryPolicyProfile) { p.PublicRoot = "https://example.invalid/public" },
		func(p *extensionv1.SkillRegistryPolicyProfile) { p.SourceID = "unregistered" },
	} {
		candidate := profile
		mutate(&candidate)
		require.ErrorIs(t, validateRemoteSkillRegistryProfile(candidate), ErrBusinessSystemPromptBundleInvalid)
	}
	profile.MaxFileCount = 100
	require.NoError(t, validateRemoteSkillRegistryProfile(profile), "bounded content policy remains owned by the plugin")
}

func TestSkillStorageRejectsNonHashPathBeforeCallingThePlugin(t *testing.T) {
	_, err := planRemoteSkillStorage(context.Background(), RemoteSkillCandidate{Version: RemoteSkillBundleVersion{EffectiveTreeSHA256: "../outside"}, Prompt: RemoteSkillPromptVersion{EffectiveSHA256: "../root"}})
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

type restrictedSkillProfileFixture struct {
	promptPolicyFixture
	bytesOnly bool
}

func (f restrictedSkillProfileFixture) InvokeOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	result, err := f.promptPolicyFixture.InvokeOperation(ctx, platform, kind, in)
	if err != nil || in.Operation != "skills.profile" {
		return result, err
	}
	var profile extensionv1.SkillRegistryPolicyProfile
	if err = json.Unmarshal(result.Payload, &profile); err != nil {
		return result, err
	}
	if f.bytesOnly {
		profile.MaxTotalBytes = 1
	} else {
		profile.MaxFileCount = 1
	}
	result.Payload, err = json.Marshal(profile)
	return result, err
}

func TestSkillProfileLimitsStopDownloadsBeforeAnyNetworkRequest(t *testing.T) {
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	for _, bytesOnly := range []bool{false, true} {
		processExtensionOperations.Store(&extensionOperationProvider{invoker: restrictedSkillProfileFixture{bytesOnly: bytesOnly}})
		client := &fakeRemoteSkillHTTPClient{}
		_, err := NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), prompt, nil)
		require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
		require.Zero(t, client.requestCount())
	}
}
