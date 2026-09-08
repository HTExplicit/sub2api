package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedModelSelectorIsolationRecognizesOnlyRoutingFields(t *testing.T) {
	for _, model := range []string{"s2pub-g23-m123", " S2PUB-g23-m123\n", "s2pub-"} {
		require.True(t, IsManagedModelSelector(model))
	}
	for _, model := range []string{"gpt-5.6-sol", "openai/gpt-5.6-sol", "my-s2pub-note", ""} {
		require.False(t, IsManagedModelSelector(model))
	}
	for _, body := range []string{
		`{"model":"s2pub-g23-m123"}`,
		`{"model":"gpt-5.6-sol","Model":" S2PUB-g23-m123 "}`,
		`{"model":"s2pub-g23-m123","model":"gpt-5.6-sol"}`,
		`{"model":"gpt-5.6-sol","session":{"MODEL":"s2pub-g23-m123"}}`,
		`{"session":{"model":"normal","model":"s2pub-g23-m123"}}`,
	} {
		require.True(t, ContainsManagedModelSelector("application/json", []byte(body)))
	}
	for _, body := range []string{
		`{"model":"gpt-5.6-sol","input":[{"model":"s2pub-is-user-data"}]}`,
		`{"model":"claude-sonnet-5","tools":[{"parameters":{"model":"s2pub-is-tool-data"}}]}`,
		`{"model":"normal","Model":"other-normal"}`,
	} {
		require.False(t, ContainsManagedModelSelector("application/json", []byte(body)))
	}
}

func TestManagedModelSelectorIsolationRequiresPublishedContextAtSelectionAndForward(t *testing.T) {
	group, account := managedRouteTestFixture(23, "verified-wire")
	selector := group.ManagedModelRoutes.Routes[0].Selector
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["private-alias-to-internal"] = selector
	for _, model := range []string{selector, " S2PUB-unknown ", "private-alias-to-internal"} {
		require.False(t, ManagedModelAccountAllowed(context.Background(), account, model))
		require.ErrorIs(t, validateManagedForwardAccount(context.Background(), nil, account, model), ErrManagedModelRouteUnavailable)
	}
	require.True(t, ManagedModelAccountAllowed(context.Background(), account, "gpt-5.6-sol"))
	require.Equal(t, "private-ssvip", account.GetMappedModel("gpt-5.6-sol"))
	require.NoError(t, validateManagedForwardAccount(context.Background(), nil, account, "gpt-5.6-sol"))
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	ctx := WithManagedModelRequest(context.Background(), request)
	require.True(t, ManagedModelAccountAllowed(ctx, account, selector), "the reserved selector is still legal after a verified managed rewrite")
	require.NoError(t, validateManagedForwardAccount(ctx, &managedLatestAccountRepo{account: account}, account, selector))
}
