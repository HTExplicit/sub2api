package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountBusinessMessageCatalogSeparatesPreviewAndFailureCodes(t *testing.T) {
	t.Parallel()

	message, ok := AccountBusinessMessage(AccountImportCodeCreate)
	require.True(t, ok)
	require.Equal(t, "account will be created", message)

	code, message := NormalizeAccountBusinessFailure(AccountImportCodeIdentityConflict)
	require.Equal(t, AccountImportCodeIdentityConflict, code)
	require.Equal(t, "account identity matches multiple existing accounts", message)
	code, message = NormalizeAccountBusinessFailure("delete_failed")
	require.Equal(t, "delete_failed", code)
	require.Equal(t, "account could not be deleted", message)

	for _, unsafe := range []string{AccountImportCodeCreate, AccountImportCodeUpdate, "unknown", ""} {
		code, message = NormalizeAccountBusinessFailure(unsafe)
		require.Equal(t, AccountJobCodeExecutionFailed, code)
		require.Equal(t, "account job item failed", message)
	}
}
