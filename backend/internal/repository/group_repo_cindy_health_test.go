//go:build unit

package repository

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupAccountAvailableSQLExcludesBothCindyTerminalStates(t *testing.T) {
	normalized := strings.ToLower(groupAccountAvailableSQL)
	require.Contains(t, normalized, "a.cindy_balance_insufficient_at is null")
	require.Contains(t, normalized, "a.cindy_banned_at is null")
}
