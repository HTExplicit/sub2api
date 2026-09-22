package service

import (
	"context"
	"strings"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type configReceiptRepository struct {
	*pluginConfigRepository
	afterWrite func()
	writes     int
}

func (r *configReceiptRepository) UpdateConfigReturningRevision(ctx context.Context, id int64, encrypted, binary string) (int64, error) {
	r.writes++
	if id != r.installation.ID || binary != r.installation.BinarySHA256 || validatePluginPrecondition(ctx, r.installation) != nil {
		return 0, ErrPluginStateChanged
	}
	if r.installation.ConfigEncrypted != encrypted {
		r.installation.Revision++
	}
	r.installation.ConfigEncrypted = encrypted
	revision := r.installation.Revision
	if r.afterWrite != nil {
		r.afterWrite()
	}
	return revision, nil
}

func configReceiptManager(t *testing.T, normalized string) (*PluginManager, *configReceiptRepository, *normalizingPluginClient, context.Context) {
	t.Helper()
	installation := &PluginInstallation{ID: 7, Revision: 4, PackageSHA256: strings.Repeat("a", 64), BinarySHA256: strings.Repeat("b", 64), ConfigEncrypted: "ENC:{}"}
	runtimeCopy := *installation
	repo := &configReceiptRepository{pluginConfigRepository: &pluginConfigRepository{installation: installation}}
	client := &normalizingPluginClient{normalized: []byte(normalized)}
	manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}, runtimes: map[int64]*pluginRuntime{7: {installation: &runtimeCopy, api: client}}}
	ctx := WithPluginExpectedPackage(WithPluginExpectedRevision(context.Background(), 4), installation.PackageSHA256)
	return manager, repo, client, ctx
}

func TestPluginSaveReceiptDoesNotRebaseOnConcurrentAdminWrite(t *testing.T) {
	manager, repo, client, ctx := configReceiptManager(t, `{"enabled":true}`)
	repo.afterWrite = func() {
		repo.installation.Revision++
		repo.installation.ConfigEncrypted = `ENC:{"other_admin":true}`
	}
	snapshot, err := manager.SaveConfigSnapshot(ctx, 7, []byte(`{"enabled":true}`))
	require.ErrorIs(t, err, ErrPluginStateChanged)
	require.Nil(t, snapshot)
	require.Equal(t, int64(6), repo.installation.Revision)
	require.Empty(t, client.applied, "a newer persistent config must not be blessed by an old canonical receipt")
}

func TestPluginSaveReceiptMatchesCanonicalAndUnchangedSaveDoesNotCancel(t *testing.T) {
	manager, repo, client, ctx := configReceiptManager(t, `{"enabled":true}`)
	snapshot, err := manager.SaveConfigSnapshot(ctx, 7, []byte(`{"enabled":false}`))
	require.NoError(t, err)
	require.Equal(t, int64(5), snapshot.Revision)
	require.Equal(t, repo.installation.PackageSHA256, snapshot.PackageSHA256)
	require.JSONEq(t, `{"enabled":true}`, string(snapshot.Config))
	require.Equal(t, []byte(snapshot.Config), client.applied)
	runtime := manager.runtimes[7]
	work, release, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer release()
	client.applied = nil
	ctx = WithPluginExpectedRevision(ctx, snapshot.Revision)
	unchanged, err := manager.SaveConfigSnapshot(ctx, 7, snapshot.Config)
	require.NoError(t, err)
	require.Equal(t, snapshot.Revision, unchanged.Revision)
	require.Equal(t, 2, repo.writes, "an unchanged save must still establish its compare-and-return receipt")
	require.Empty(t, client.applied)
	require.NoError(t, work.Err())
}

func TestPluginStaleUIActionAndJobPackageRejectedBeforeRuntime(t *testing.T) {
	manager, repo, _ := hostIOAdmissionManager(t)
	repo.current.Manifest.Contributions = nil
	ctx := WithPluginExpectedPackage(context.Background(), strings.Repeat("c", 64))
	_, err := manager.InvokeAdminExtension(ctx, repo.current.ID, 0, "proxy.test", []byte(`{}`))
	require.ErrorIs(t, err, ErrPluginStateChanged)
	_, _, err = manager.BindAccountJobExecution(ctx, repo.current.ID, 0)
	require.ErrorIs(t, err, ErrPluginStateChanged)
	require.Zero(t, repo.holds)
}

type configReceiptRaceClient struct {
	*normalizingPluginClient
	validated func()
	applied   func()
}

func (c *configReceiptRaceClient) ValidateConfig(ctx context.Context, request *pluginv1.ValidateConfigRequest, options ...grpc.CallOption) (*pluginv1.ValidateConfigResponse, error) {
	if c.validated != nil {
		c.validated()
	}
	return c.normalizingPluginClient.ValidateConfig(ctx, request, options...)
}
func (c *configReceiptRaceClient) ApplyConfig(ctx context.Context, request *pluginv1.ApplyConfigRequest, options ...grpc.CallOption) (*pluginv1.ApplyConfigResponse, error) {
	result, err := c.normalizingPluginClient.ApplyConfig(ctx, request, options...)
	if c.applied != nil {
		c.applied()
	}
	return result, err
}

func TestPluginUnchangedConfigStillRejectsConcurrentReadVersion(t *testing.T) {
	manager, repo, client, ctx := configReceiptManager(t, `{}`)
	runtime := manager.runtimes[7]
	work, release, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer release()
	runtime.api = &configReceiptRaceClient{normalizingPluginClient: client, validated: func() { repo.installation.Revision++ }}
	snapshot, err := manager.SaveConfigSnapshot(ctx, 7, []byte(`{}`))
	require.ErrorIs(t, err, ErrPluginStateChanged)
	require.Nil(t, snapshot)
	require.Equal(t, 1, repo.writes)
	require.Empty(t, client.applied)
	require.NoError(t, work.Err())
}

func TestPluginSaveReceiptRemainsOwnVersionIfAnotherAdminWritesDuringApply(t *testing.T) {
	manager, repo, client, ctx := configReceiptManager(t, `{"mine":true}`)
	manager.runtimes[7].api = &configReceiptRaceClient{normalizingPluginClient: client, applied: func() {
		repo.installation.Revision++
		repo.installation.ConfigEncrypted = `ENC:{"other":true}`
	}}
	snapshot, err := manager.SaveConfigSnapshot(ctx, 7, []byte(`{"mine":true}`))
	require.NoError(t, err)
	require.Equal(t, int64(5), snapshot.Revision)
	require.Equal(t, int64(6), repo.installation.Revision)
	require.JSONEq(t, `{"mine":true}`, string(snapshot.Config))
	_, err = manager.SaveConfigSnapshot(WithPluginExpectedRevision(ctx, snapshot.Revision), 7, snapshot.Config)
	require.ErrorIs(t, err, ErrPluginStateChanged, "later save cannot borrow the other administrator's revision")
}
