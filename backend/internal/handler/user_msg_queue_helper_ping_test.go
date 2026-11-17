package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestUserMsgQueuePingInterval(t *testing.T) {
	for _, tc := range []struct {
		name           string
		interval, want time.Duration
	}{
		{"disabled", 0, 0},
		{"default", -time.Second, defaultPingInterval},
		{"configured", time.Second, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := NewUserMsgQueueHelper(nil, SSEPingFormatClaude, tc.interval)
			require.Equal(t, tc.want, helper.pingInterval)
		})
	}
}

func TestUserMsgQueueWaitWithoutPingCancelsBeforeStartingStream(t *testing.T) {
	for _, format := range []SSEPingFormat{SSEPingFormatClaude, SSEPingFormatComment} {
		t.Run(string(format), func(t *testing.T) {
			helper := NewUserMsgQueueHelper(nil, format, 0)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			ctx, cancel := context.WithCancel(c.Request.Context())
			cancel()
			started := false
			release, err := helper.waitForLockWithPing(c, ctx, 1, 1, true, &started, zap.NewNop())
			require.ErrorContains(t, err, "umq wait timeout")
			require.Nil(t, release)
			require.False(t, started)
			require.False(t, c.Writer.Written())
			require.False(t, recorder.Flushed)
			require.Empty(t, recorder.Body.String())
			require.Empty(t, recorder.Header().Get("Content-Type"))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "busy"})
			require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		})
	}
}
