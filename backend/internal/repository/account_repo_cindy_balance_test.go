package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCindyInsufficientCandidateIDsProtectNonCindyAndManuallyStoppedAccounts(t *testing.T) {
	markedAt := time.Now()
	cindy := map[string]any{"base_url": "https://api.laxarouter.ai"}
	candidates := []*dbAccountCandidate{
		{ID: 5, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: cindy, MarkedAt: &markedAt},
		{ID: 2, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: cindy, MarkedAt: &markedAt},
		{ID: 6, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, Status: service.StatusDisabled, Schedulable: true, Credentials: cindy, MarkedAt: &markedAt},
		{ID: 7, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: false, Credentials: cindy, MarkedAt: &markedAt},
		{ID: 8, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai/v1"}, MarkedAt: &markedAt},
		{ID: 9, Platform: service.PlatformCindy, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: cindy},
		{ID: 10, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: cindy, MarkedAt: &markedAt},
	}

	require.Equal(t, []int64{2, 5}, cindyInsufficientCandidateIDs(candidates))
}
