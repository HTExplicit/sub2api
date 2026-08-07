package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func openAILoadOrderPlanForTest(accounts []*Account, loads map[int64]*AccountLoadInfo) openAIAccountLoadPlan {
	scheduler := &defaultOpenAIAccountScheduler{}
	return scheduler.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{}, accounts, loads)
}

func openAISelectionIDsForTest(plan openAIAccountLoadPlan) []int64 {
	ids := make([]int64, 0, len(plan.selectionOrder))
	for _, candidate := range plan.selectionOrder {
		if candidate.account != nil {
			ids = append(ids, candidate.account.ID)
		}
	}
	return ids
}

func TestBuildOpenAIAccountLoadPlan_UsesExactConcurrencyRatioBeforeWaiting(t *testing.T) {
	accounts := []*Account{
		{ID: 2, Priority: 0, Concurrency: 10},
		{ID: 1, Priority: 0, Concurrency: 3},
	}
	loads := map[int64]*AccountLoadInfo{
		2: {AccountID: 2, CurrentConcurrency: 3, LoadRate: 30}, // 3/10
		1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 33}, // 1/3; exact comparison is 3/10 < 1/3
	}

	plan := openAILoadOrderPlanForTest(accounts, loads)
	require.Equal(t, []int64{2, 1}, openAISelectionIDsForTest(plan))
}

func TestBuildOpenAIAccountLoadPlan_DoesNotHideLightLoadBehindIntegerTruncation(t *testing.T) {
	accounts := []*Account{
		{ID: 1, Priority: 0, Concurrency: 100},
		{ID: 2, Priority: 0, Concurrency: 3},
	}
	loads := map[int64]*AccountLoadInfo{
		1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 1}, // 1/100
		2: {AccountID: 2, CurrentConcurrency: 0, LoadRate: 0}, // 0/3
	}

	plan := openAILoadOrderPlanForTest(accounts, loads)
	require.Equal(t, []int64{2, 1}, openAISelectionIDsForTest(plan))
}

func TestBuildOpenAIAccountLoadPlan_UsesWaitingCountAfterLoadRatio(t *testing.T) {
	accounts := []*Account{
		{ID: 1, Priority: 0, Concurrency: 4},
		{ID: 2, Priority: 0, Concurrency: 4},
	}
	loads := map[int64]*AccountLoadInfo{
		1: {AccountID: 1, CurrentConcurrency: 1, WaitingCount: 2},
		2: {AccountID: 2, CurrentConcurrency: 1, WaitingCount: 0},
	}

	plan := openAILoadOrderPlanForTest(accounts, loads)
	require.Equal(t, []int64{2, 1}, openAISelectionIDsForTest(plan))
}

func TestBuildOpenAIAccountLoadPlan_UsesOldestLastUsedAtAfterWaitingCount(t *testing.T) {
	now := time.Now()
	older := now.Add(-2 * time.Hour)
	recent := now.Add(-time.Hour)
	accounts := []*Account{
		{ID: 1, Priority: 0, Concurrency: 4, LastUsedAt: &recent},
		{ID: 2, Priority: 0, Concurrency: 4, LastUsedAt: &older},
	}
	loads := map[int64]*AccountLoadInfo{
		1: {AccountID: 1, CurrentConcurrency: 1},
		2: {AccountID: 2, CurrentConcurrency: 1},
	}

	plan := openAILoadOrderPlanForTest(accounts, loads)
	require.Equal(t, []int64{2, 1}, openAISelectionIDsForTest(plan))
}

func TestBuildOpenAIAccountLoadPlan_NilLastUsedAtIsOldestAndIDBreaksTies(t *testing.T) {
	used := time.Now().Add(-time.Hour)
	accounts := []*Account{
		{ID: 3, Priority: 0, Concurrency: 4},
		{ID: 1, Priority: 0, Concurrency: 4},
		{ID: 2, Priority: 0, Concurrency: 4, LastUsedAt: &used},
	}
	loads := map[int64]*AccountLoadInfo{
		1: {AccountID: 1, CurrentConcurrency: 1},
		2: {AccountID: 2, CurrentConcurrency: 1},
		3: {AccountID: 3, CurrentConcurrency: 1},
	}

	plan := openAILoadOrderPlanForTest(accounts, loads)
	require.Equal(t, []int64{1, 3, 2}, openAISelectionIDsForTest(plan))
}
