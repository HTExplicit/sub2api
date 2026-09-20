package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPluginResourceNestedSelectionCannotBecomeUnrestricted(t *testing.T) {
	for _, test := range []struct {
		body              string
		ids               []int64
		filtered, invalid bool
	}{
		{`{"scope":{"mode":"selected","account_ids":[9],"filters":{"account_ids":[99]}}}`, []int64{9}, false, false},
		{`{"scope":{"mode":"selected","filters":{"account_ids":[99]}}}`, []int64{99}, false, false},
		{`{"scope":{"mode":"selected","account_ids":[]}}`, nil, false, true},
		{`{"scope":{"mode":"all"}}`, nil, true, false},
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/fixture", strings.NewReader(test.body))
		ids, filtered, err := resourceAccountTargets(ctx, extensionv1.ResourceDescriptor{AccountScopeField: "scope"})
		if test.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, test.ids, ids)
		require.Equal(t, test.filtered, filtered)
	}
}
