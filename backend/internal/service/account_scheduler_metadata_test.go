package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func managedPoolProjectionFixture(t *testing.T, platform string) (*Group, *Account, *Account, context.Context) {
	t.Helper()
	group, full := managedRouteTestFixture(33, "verified-pool-model")
	group.Platform = platform
	group.ManagedModelRoutes.Routes[0].TargetPlatform = platform
	full.Platform = platform
	full.Credentials["pool_mode"] = true
	full.Credentials["pool_mode_retry_count"] = 3
	full.Credentials["provider_extension"] = nil
	full.Credentials["header_overrides"] = map[string]any{"X-Fixture": "preserve"}
	group.ManagedModelRoutes.Routes[0].Accounts[0].AccountFingerprint = ManagedModelAccountFingerprint(full)
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	ctx := WithManagedModelRequest(context.Background(), request)
	projection := *full
	projection.Credentials = map[string]any{
		"api_key": full.Credentials["api_key"], "base_url": full.Credentials["base_url"], "model_mapping": full.Credentials["model_mapping"],
	}
	return group, full, &projection, ctx
}

func TestManagedSchedulerMetadataRestoresPoolAccountCandidateEligibility(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			group, full, projection, ctx := managedPoolProjectionFixture(t, platform)
			selector := group.ManagedModelRoutes.Routes[0].Selector
			fingerprint := ManagedModelAccountFingerprint(full)
			require.NotEqual(t, fingerprint, ManagedModelAccountFingerprint(projection), "the old metadata projection loses pool/header/null identity fields")
			require.False(t, ManagedModelAccountAllowed(ctx, projection, selector), "reproduce the former pre-hydration rejection")
			projection.SchedulerMetadata = &AccountSchedulerMetadata{Version: AccountSchedulerMetadataVersion, IdentityFingerprint: fingerprint}
			require.True(t, ValidSchedulerMetadataIdentity(projection))
			require.Empty(t, ManagedModelAccountFingerprint(projection), "a projection cannot issue a new full-account proof")
			require.True(t, ManagedModelAccountAllowed(ctx, projection, selector))
			require.True(t, (&GatewayService{}).isModelSupportedByAccountWithContext(ctx, projection, selector))
			if platform == PlatformOpenAI {
				eligible, reason := openAICompatibleAccountEligibilityBeforeProfit(ctx, projection, platform, selector, false, OpenAIEndpointCapabilityResponses)
				require.True(t, eligible, reason)
			}
			require.Equal(t, fingerprint, ManagedModelAccountFingerprint(full))
			require.True(t, ManagedModelAccountAllowed(ctx, full, selector))
		})
	}
}

func TestManagedSchedulerMetadataCannotBypassMemberOrRouteChecks(t *testing.T) {
	group, full, projection, ctx := managedPoolProjectionFixture(t, PlatformOpenAI)
	selector := group.ManagedModelRoutes.Routes[0].Selector
	projection.SchedulerMetadata = &AccountSchedulerMetadata{Version: AccountSchedulerMetadataVersion, IdentityFingerprint: ManagedModelAccountFingerprint(full)}
	require.False(t, ManagedModelAccountAllowed(ctx, projection, "gpt-5.6-sol"), "public/private original names cannot replace the group's selector")
	projection.ID++
	require.False(t, ManagedModelAccountAllowed(ctx, projection, selector))
	projection.ID--
	projection.Platform = PlatformAnthropic
	require.False(t, ManagedModelAccountAllowed(ctx, projection, selector))
	projection.Platform = PlatformOpenAI
	projection.Credentials = map[string]any{"model_mapping": map[string]any{selector: "untested-target"}}
	require.False(t, ManagedModelAccountAllowed(ctx, projection, selector))
	projection.Credentials = map[string]any{"model_mapping": full.Credentials["model_mapping"]}
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	request.Route.Accounts = append([]ManagedModelRouteAccount(nil), request.Route.Accounts...)
	request.Route.Accounts[0].Endpoints = []string{CompositeRouteEndpointMessages}
	require.False(t, ManagedModelAccountAllowed(WithManagedModelRequest(context.Background(), request), projection, selector))
	for _, invalid := range []*AccountSchedulerMetadata{
		{Version: 0, IdentityFingerprint: projection.SchedulerMetadata.IdentityFingerprint},
		{Version: 1},
		{Version: 1, IdentityFingerprint: strings.Repeat("Z", 64)},
	} {
		projection.SchedulerMetadata = invalid
		require.False(t, ManagedModelAccountAllowed(ctx, projection, selector))
	}
}

func TestManagedSchedulerMetadataNeverReplacesFullForwardValidation(t *testing.T) {
	group, full, projection, ctx := managedPoolProjectionFixture(t, PlatformOpenAI)
	selector := group.ManagedModelRoutes.Routes[0].Selector
	projection.SchedulerMetadata = &AccountSchedulerMetadata{Version: AccountSchedulerMetadataVersion, IdentityFingerprint: ManagedModelAccountFingerprint(full)}
	repo := &managedLatestAccountRepo{account: full}
	require.True(t, full.IsPoolMode())
	require.NoError(t, validateManagedForwardAccount(ctx, repo, full, selector), "normal pool policy does not mutate the full identity")
	require.ErrorIs(t, validateManagedForwardAccount(ctx, repo, projection, selector), ErrManagedModelRouteUnavailable, "the chosen account must have been hydrated before forwarding")
	repo.account = projection
	require.ErrorIs(t, validateManagedForwardAccount(ctx, repo, full, selector), ErrManagedModelRouteUnavailable, "a repository must not substitute metadata for the final complete read")
	latest := *full
	latest.Credentials = make(map[string]any, len(full.Credentials))
	for key, value := range full.Credentials {
		latest.Credentials[key] = value
	}
	latest.Credentials["pool_mode_retry_count"] = 4
	repo.account = &latest
	require.ErrorIs(t, validateManagedForwardAccount(ctx, repo, full, selector), ErrManagedModelRouteUnavailable, "a stale cached digest cannot authorize changed complete configuration")
}
