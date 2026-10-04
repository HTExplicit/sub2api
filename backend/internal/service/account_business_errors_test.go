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

	code, message := AccountJobFailure(AccountImportCodeIdentityConflict, "")
	require.Equal(t, AccountImportCodeIdentityConflict, code)
	require.Equal(t, "account identity matches multiple existing accounts", message)
	code, message = AccountJobFailure("delete_failed", "")
	require.Equal(t, "delete_failed", code)
	require.Equal(t, "account could not be deleted", message)

	// The underlying error text is kept verbatim; the catalog sentence is only
	// the fallback for failures without one. Reported codes are never renamed.
	code, message = AccountJobFailure("delete_failed", "pq: account 7 is referenced by usage_logs")
	require.Equal(t, "delete_failed", code)
	require.Equal(t, "pq: account 7 is referenced by usage_logs", message)
	// A code without a catalog sentence is kept as reported and gets the
	// generic item sentence.
	code, message = AccountJobFailure("code_not_in_catalog", "")
	require.Equal(t, "code_not_in_catalog", code)
	require.Equal(t, "account job item failed", message)

	// Preview success codes never lend their success sentence to a failure.
	for _, previewCode := range []string{AccountImportCodeCreate, AccountImportCodeUpdate} {
		code, message = AccountJobFailure(previewCode, "")
		require.Equal(t, previewCode, code)
		require.Equal(t, "account job item failed", message)
	}
	code, message = AccountJobFailure("", "")
	require.Equal(t, AccountJobCodeExecutionFailed, code)
	require.Equal(t, "account job item failed", message)
}
