//go:build unit

package schema_test

import (
	"testing"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/ent/userplatformquota"
	"github.com/stretchr/testify/require"
)

func TestUserPlatformQuotaValidatorAllowsCanonicalCindy(t *testing.T) {
	require.NotNil(t, userplatformquota.PlatformValidator)
	require.NoError(t, userplatformquota.PlatformValidator("cindy"))
	require.Error(t, userplatformquota.PlatformValidator("unknown"))
}
