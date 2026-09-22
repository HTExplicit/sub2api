package repository

import (
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestPluginRegistrySerializationConflictMappingPreservesOtherErrors(t *testing.T) {
	conflict := &pgconn.PgError{Code: "40001", Message: "fixture serialization conflict"}
	require.ErrorIs(t, pluginRegistryTxError(conflict), service.ErrPluginStateChanged)
	require.ErrorIs(t, pluginRegistryTxError(fmt.Errorf("statement: %w", conflict)), service.ErrPluginStateChanged)
	require.ErrorIs(t, pluginRegistryTxError(service.ErrPluginStateChanged), service.ErrPluginStateChanged)
	constraint := &pgconn.PgError{Code: "23505", Message: "fixture constraint"}
	require.Same(t, constraint, pluginRegistryTxError(constraint), "unrelated errors must not turn into hidden automatic retries")
	require.NoError(t, pluginRegistryTxError(nil))
}
