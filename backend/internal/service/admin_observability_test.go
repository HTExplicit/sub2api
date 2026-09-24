package service

import (
	"context"
	"strconv"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type trafficScopeCache struct {
	beginIDs    []int64
	snapshotIDs []int64
	outcomes    []AccountTrafficOutcome
}

func (c *trafficScopeCache) Begin(_ context.Context, id int64, _ AccountTrafficProtocol) error {
	c.beginIDs = append(c.beginIDs, id)
	return nil
}
func (c *trafficScopeCache) Finish(_ context.Context, _ int64, _ AccountTrafficProtocol, outcome AccountTrafficOutcome) error {
	c.outcomes = append(c.outcomes, outcome)
	return nil
}
func (c *trafficScopeCache) Snapshot(_ context.Context, id int64) (map[AccountTrafficProtocol]AccountTrafficObserveState, error) {
	c.snapshotIDs = append(c.snapshotIDs, id)
	return map[AccountTrafficProtocol]AccountTrafficObserveState{AccountTrafficProtocolHTTP: {Started: 1}}, nil
}

func useAdminObservability(t *testing.T, config extensionv1.AdminObservabilityConfig) {
	t.Helper()
	previous := adminObservabilityConfigOverride.Load()
	t.Cleanup(func() { adminObservabilityConfigOverride.Store(previous) })
	ConfigureAdminObservability(&config)
}

func TestTrafficObservationFollowsTelemetrySwitch(t *testing.T) {
	account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			useAdminObservability(t, extensionv1.AdminObservabilityConfig{TelemetryEnabled: enabled})
			cache := &trafficScopeCache{}
			observer := NewAccountTrafficObserver(cache, nil)
			require.Equal(t, enabled, observer.Enabled())
			turn := observer.Begin(context.Background(), account, AccountTrafficProtocolWS)
			snapshot, err := observer.Snapshot(context.Background(), account)
			if !enabled {
				require.Nil(t, turn)
				require.ErrorIs(t, err, ErrAccountTrafficTelemetryUnavailable)
				require.Empty(t, cache.beginIDs)
				require.Empty(t, cache.snapshotIDs)
				return
			}
			require.NotNil(t, turn)
			require.NoError(t, err)
			require.EqualValues(t, 1, snapshot[AccountTrafficProtocolHTTP].Started)
			require.Equal(t, []int64{2}, cache.beginIDs)
			require.Equal(t, []int64{2}, cache.snapshotIDs)
		})
	}
}

func TestTrafficObservationRejectsMissingAccountIdentity(t *testing.T) {
	useAdminObservability(t, extensionv1.AdminObservabilityConfig{TelemetryEnabled: true})
	cache := &trafficScopeCache{}
	observer := NewAccountTrafficObserver(cache, nil)
	for _, account := range []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{ID: 2, Type: AccountTypeOAuth},
		{ID: 2, Platform: PlatformOpenAI},
		{ID: 2, Platform: "*", Type: AccountTypeOAuth},
		{ID: 2, Platform: PlatformOpenAI, Type: "*"},
	} {
		require.Nil(t, observer.Begin(context.Background(), account, AccountTrafficProtocolHTTP))
		snapshot, err := observer.Snapshot(context.Background(), account)
		require.ErrorIs(t, err, ErrAccountTrafficTelemetryUnavailable)
		require.Nil(t, snapshot)
	}
	require.Empty(t, cache.beginIDs)
	require.Empty(t, cache.snapshotIDs)
}

func TestTrafficObservationFinishesStartedTurnAfterTelemetryIsSwitchedOff(t *testing.T) {
	useAdminObservability(t, extensionv1.AdminObservabilityConfig{TelemetryEnabled: true})
	cache := &trafficScopeCache{}
	observer := NewAccountTrafficObserver(cache, nil)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	turn := observer.Begin(context.Background(), account, AccountTrafficProtocolHTTP)
	require.NotNil(t, turn)
	ConfigureAdminObservability(&extensionv1.AdminObservabilityConfig{})
	turn.Finish(&OpenAIForwardResult{}, nil, false)
	turn.Finish(nil, nil, false)
	require.Equal(t, []AccountTrafficOutcome{AccountTrafficOutcomeCompleted2xx}, cache.outcomes)
	require.Nil(t, observer.Begin(context.Background(), account, AccountTrafficProtocolHTTP))
	require.Len(t, cache.beginIDs, 1)
}

func TestAccountTrafficOutcomeRulesKeepCancellationAboveErrorsAndRequireWSTerminals(t *testing.T) {
	require.NoError(t, accountTrafficOutcomeRules.Validate())
	for _, tc := range []struct {
		facts map[string]string
		want  AccountTrafficOutcome
	}{
		{map[string]string{"client_cancelled": "true", "has_error": "true", "error_status": "503"}, AccountTrafficOutcomeCancelled},
		{map[string]string{"ws": "true", "terminal": "response.incomplete", "has_error": "true", "error_status": "429"}, AccountTrafficOutcomeCancelled},
		{map[string]string{"ws": "true", "has_result": "true", "has_error": "false", "terminal": ""}, AccountTrafficOutcomeFailedOther},
		{map[string]string{"ws": "true", "has_result": "true", "has_error": "false", "terminal": "response.failed", "terminal_status": "503"}, AccountTrafficOutcomeUpstream5xx},
		{map[string]string{"ws": "false", "has_result": "true", "has_error": "false"}, AccountTrafficOutcomeCompleted2xx},
	} {
		got, err := accountTrafficOutcomeRules.Evaluate(tc.facts)
		require.NoError(t, err)
		require.Equal(t, string(tc.want), got)
	}
}
