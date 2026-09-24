package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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

// restrictedSkillProfile lowers one registry profile limit returned by invoke.
func restrictedSkillProfile(invoke func(context.Context, extensionv1.Invocation) (extensionv1.Result, error), bytesOnly bool) func(context.Context, extensionv1.Invocation) (extensionv1.Result, error) {
	return func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		result, err := invoke(ctx, in)
		if err != nil || in.Operation != "skills.profile" {
			return result, err
		}
		var profile extensionv1.SkillRegistryPolicyProfile
		if err = json.Unmarshal(result.Payload, &profile); err != nil {
			return result, err
		}
		if bytesOnly {
			profile.MaxTotalBytes = 1
		} else {
			profile.MaxFileCount = 1
		}
		result.Payload, err = json.Marshal(profile)
		return result, err
	}
}

func TestSkillProfileLimitsStopDownloadsBeforeAnyNetworkRequest(t *testing.T) {
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	previous := invokePromptSkills
	t.Cleanup(func() { invokePromptSkills = previous })
	for _, bytesOnly := range []bool{false, true} {
		invokePromptSkills = restrictedSkillProfile(previous, bytesOnly)
		client := &fakeRemoteSkillHTTPClient{}
		_, err := NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), prompt, nil)
		require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
		require.Zero(t, client.requestCount())
	}
}
