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

	code, message := NormalizeAccountBusinessFailure(AccountImportCodeCindyTargetRequired)
	require.Equal(t, AccountImportCodeCindyTargetRequired, code)
	require.Equal(t, "one explicit target group is required for Cindy imports", message)

	for _, unsafe := range []string{AccountImportCodeCreate, AccountImportCodeUpdate, "unknown", ""} {
		code, message = NormalizeAccountBusinessFailure(unsafe)
		require.Equal(t, AccountJobCodeExecutionFailed, code)
		require.Equal(t, "account job item failed", message)
	}
}
