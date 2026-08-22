package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type cindyOpaqueBindingLookupStore struct {
	OpenAIWSStateStore
	accountIDs map[string]int64
	errors     map[string]error
}

func (s *cindyOpaqueBindingLookupStore) GetResponseAccount(_ context.Context, _ int64, responseID string) (int64, error) {
	if err := s.errors[responseID]; err != nil {
		return 0, err
	}
	return s.accountIDs[responseID], nil
}

func TestLookupCindyOpaqueContinuationBindingUsesAnyUnanimousSurvivingBinding(t *testing.T) {
	tests := []struct {
		name       string
		bindingIDs []string
		accountIDs map[string]int64
	}{
		{
			name:       "miss before hit",
			bindingIDs: []string{"z_hit", "a_miss"},
			accountIDs: map[string]int64{"z_hit": 731},
		},
		{
			name:       "hit before miss",
			bindingIDs: []string{"z_miss", "a_hit"},
			accountIDs: map[string]int64{"a_hit": 731},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := LookupCindyOpaqueContinuationBinding(
				context.Background(),
				&cindyOpaqueBindingLookupStore{accountIDs: tt.accountIDs},
				17,
				tt.bindingIDs,
			)

			require.Equal(t, OpenAIContinuationBindingHit, lookup.State)
			require.Equal(t, int64(731), lookup.AccountID)
			require.NoError(t, lookup.Err)
		})
	}
}

func TestLookupCindyOpaqueContinuationBindingPreservesMissConflictAndStoreError(t *testing.T) {
	t.Run("all miss", func(t *testing.T) {
		lookup := LookupCindyOpaqueContinuationBinding(
			context.Background(),
			&cindyOpaqueBindingLookupStore{accountIDs: map[string]int64{}},
			17,
			[]string{"a_miss", "z_miss"},
		)

		require.Equal(t, OpenAIContinuationBindingMiss, lookup.State)
		require.Zero(t, lookup.AccountID)
		require.NoError(t, lookup.Err)
	})

	t.Run("conflicting hits", func(t *testing.T) {
		lookup := LookupCindyOpaqueContinuationBinding(
			context.Background(),
			&cindyOpaqueBindingLookupStore{accountIDs: map[string]int64{"a_hit": 731, "z_hit": 732}},
			17,
			[]string{"z_hit", "a_hit"},
		)

		require.Equal(t, OpenAIContinuationBindingStoreError, lookup.State)
		require.Zero(t, lookup.AccountID)
		require.ErrorIs(t, lookup.Err, errCindyOpaqueBindingConflict)
	})

	storeErr := errors.New("redis unavailable")
	for _, tt := range []struct {
		name       string
		bindingIDs []string
		accountIDs map[string]int64
		errors     map[string]error
	}{
		{
			name:       "store error before hit",
			bindingIDs: []string{"z_hit", "a_error"},
			accountIDs: map[string]int64{"z_hit": 731},
			errors:     map[string]error{"a_error": storeErr},
		},
		{
			name:       "store error after hit",
			bindingIDs: []string{"z_error", "a_hit"},
			accountIDs: map[string]int64{"a_hit": 731},
			errors:     map[string]error{"z_error": storeErr},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lookup := LookupCindyOpaqueContinuationBinding(
				context.Background(),
				&cindyOpaqueBindingLookupStore{accountIDs: tt.accountIDs, errors: tt.errors},
				17,
				tt.bindingIDs,
			)

			require.Equal(t, OpenAIContinuationBindingStoreError, lookup.State)
			require.Zero(t, lookup.AccountID)
			require.ErrorIs(t, lookup.Err, storeErr)
		})
	}
}
