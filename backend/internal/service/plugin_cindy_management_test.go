package service

import (
	"context"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type capturedCindyManagement struct {
	promptPolicyFixture
	payloads []string
}

func (f *capturedCindyManagement) InvokeOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation == "cindy.duplicates.plan" {
		f.payloads = append(f.payloads, string(in.Payload))
	}
	return f.promptPolicyFixture.InvokeOperation(ctx, platform, kind, in)
}

func TestCindyManagementKeepsCredentialsInHostAndStopsWhenProviderDisabled(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	fixture := &capturedCindyManagement{}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	accounts := []Account{{ID: 1, Platform: PlatformCindy, Type: AccountTypeAPIKey, Status: StatusActive, Credentials: map[string]any{"api_key": "private-key-material", "base_url": "https://api.laxarouter.ai"}}}
	duplicate := accounts[0]
	duplicate.ID = 2
	accounts = append(accounts, duplicate)
	groups, err := buildCindyDuplicateIdentityInventory(context.Background(), accounts)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Len(t, fixture.payloads, 1)
	require.NotContains(t, fixture.payloads[0], "private-key-material")
	require.NotContains(t, fixture.payloads[0], "api.laxarouter.ai")
	processExtensionOperations.Store(nil)
	_, err = buildCindyDuplicateIdentityInventory(context.Background(), accounts)
	require.ErrorIs(t, err, ErrCindyGroupAdminUnavailable)
	_, err = PlanCindyGroupPartition(context.Background(), 1, 1, "cindy")
	require.ErrorIs(t, err, ErrCindyGroupAdminUnavailable)
}
