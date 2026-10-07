//go:build unit

package handler

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestCodexGatewayBorrowWSPreparationFailureDoesNotReportTargetHealth(t *testing.T) {
	local := &service.CodexGatewayBorrowFailure{Cause: errors.New("dial tcp synthetic-source.invalid:443: connect: connection refused")}
	require.False(t, shouldReportOpenAIWSProxyAccountFailure(local))
	classified := service.NewCodexGatewayBorrowRequestFailure(local)
	require.True(t, classified.ShouldRetryNextAccount())
	require.False(t, shouldReportOpenAIWSProxyAccountFailure(classified))
	closed := service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "local preparation unavailable", classified)
	require.False(t, shouldReportOpenAIWSProxyAccountFailure(closed))
	require.True(t, shouldReportOpenAIWSProxyAccountFailure(errors.New("ordinary business websocket connection refused")))
}
