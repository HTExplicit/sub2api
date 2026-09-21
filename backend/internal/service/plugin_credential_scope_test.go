package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type credentialScopeDirectory struct{ calls int }

func (d *credentialScopeDirectory) ReadExtensionAccount(context.Context, int64) (*extensionv1.Account, error) {
	return &extensionv1.Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, nil
}
func (d *credentialScopeDirectory) ListExtensionAccounts(context.Context, extensionv1.AccountQuery) ([]extensionv1.Account, error) {
	return nil, nil
}
func (d *credentialScopeDirectory) ResolveExtensionIdentity(context.Context, extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error) {
	d.calls++
	return &extensionv1.OutboundIdentity{AccountID: 7, Token: "synthetic-only"}, nil
}

func TestRequestPolicyCapabilityDoesNotImplyCredentialAccess(t *testing.T) {
	directory := &credentialScopeDirectory{}
	installation := &PluginInstallation{Manifest: PluginManifest{Capabilities: []PluginCapability{{ID: extensionv1.CapabilityRequest, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}}}
	host := &pluginExtensionHost{state: &extensionReadOnlyFixture{}, directory: directory, installation: installation}
	raw, _ := json.Marshal(extensionv1.AccountQuery{AccountID: 7})
	_, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostAccountRead, Payload: raw})
	require.NoError(t, err)
	_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostResolveIdentity, Payload: raw})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Zero(t, directory.calls)
	installation.Manifest.Capabilities = append(installation.Manifest.Capabilities, PluginCapability{ID: extensionv1.CapabilityCredentials, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
	result, err := host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostResolveIdentity, Payload: raw})
	require.NoError(t, err)
	require.Equal(t, 1, directory.calls)
	require.Contains(t, string(result.Payload), "synthetic-only")
	host.allows = func(capability, _, _ string, _ int64) bool { return capability != extensionv1.CapabilityCredentials }
	_, err = host.Call(context.Background(), extensionv1.HostInvocation{Operation: extensionv1.HostResolveIdentity, Payload: raw})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "a declaration cannot override a revoked live capability")
	require.Equal(t, 1, directory.calls)
}
