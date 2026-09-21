//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginConfigReturningReceiptRejectsOldDraftAndDoesNotBorrowLaterRevision(t *testing.T) {
	ctx := context.Background()
	repo, current, _ := installedUpdateFixture(t)
	oldDraft := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, current.Revision), current.PackageSHA256)
	revision, err := repo.UpdateConfigReturningRevision(oldDraft, current.ID, "first-canonical-cipher", current.BinarySHA256)
	require.NoError(t, err)
	require.Equal(t, current.Revision+1, revision)
	secondDraft := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, revision), current.PackageSHA256)
	secondRevision, err := repo.UpdateConfigReturningRevision(secondDraft, current.ID, "second-admin-cipher", current.BinarySHA256)
	require.NoError(t, err)
	require.Equal(t, revision+1, secondRevision)
	require.Equal(t, current.Revision+1, revision, "the first RETURNING receipt remains the first write's version")
	_, err = repo.UpdateConfigReturningRevision(oldDraft, current.ID, "old-tab-cipher", current.BinarySHA256)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	wrongPackage := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, secondRevision), strings.Repeat("f", 64))
	_, err = repo.UpdateConfigReturningRevision(wrongPackage, current.ID, "wrong-package-cipher", current.BinarySHA256)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	unchanged := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, secondRevision), current.PackageSHA256)
	unchangedRevision, err := repo.UpdateConfigReturningRevision(unchanged, current.ID, "second-admin-cipher", current.BinarySHA256)
	require.NoError(t, err)
	require.Equal(t, secondRevision, unchangedRevision, "same encrypted configuration is a CAS receipt without revision inflation")
	latest, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, "second-admin-cipher", latest.ConfigEncrypted)
	require.Equal(t, secondRevision, latest.Revision)
}

func TestPluginDeleteUsesReadTimeRevisionAndPackageWithinGraphTransaction(t *testing.T) {
	ctx := context.Background()
	repo, current, _ := installedUpdateFixture(t)
	for index := range current.Bindings {
		current.Bindings[index].Enabled = false
	}
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateDisabled, "", nil, current.State, current.BinarySHA256))
	disabled, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	oldDraft := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, disabled.Revision), disabled.PackageSHA256)
	require.NoError(t, repo.UpdateConfig(ctx, current.ID, "admin-edited-before-delete", current.BinarySHA256))
	require.ErrorIs(t, repo.Delete(oldDraft, current.ID, current.BinarySHA256), service.ErrPluginStateChanged)
	latest, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	wrongPackage := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, latest.Revision), strings.Repeat("f", 64))
	require.ErrorIs(t, repo.Delete(wrongPackage, current.ID, current.BinarySHA256), service.ErrPluginStateChanged)
	confirmed := service.WithPluginExpectedPackage(service.WithPluginExpectedRevision(ctx, latest.Revision), latest.PackageSHA256)
	require.NoError(t, repo.Delete(confirmed, current.ID, current.BinarySHA256))
}
