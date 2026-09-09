package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type managedModelAffinityTestCache struct {
	GatewayCache
	ManagedModelAffinityCache
}

type managedModelAffinityTestLegacyStore struct {
	OpenAIWSStateStore
	ownerKnown   bool
	ownerKey     int64
	ownerUser    int64
	accountID    int64
	ownerError   error
	accountError error
	accountReads int
}

func (s *managedModelAffinityTestLegacyStore) GetHTTPResponseOwner(context.Context, int64, string) (int64, int64, bool, error) {
	return s.ownerUser, s.ownerKey, s.ownerKnown, s.ownerError
}

func (s *managedModelAffinityTestLegacyStore) GetResponseAccount(context.Context, int64, string) (int64, error) {
	s.accountReads++
	return s.accountID, s.accountError
}

func TestManagedModelAffinityCacheAccessorAndLegacyLookup(t *testing.T) {
	var gateway *GatewayService
	require.Nil(t, gateway.ManagedModelAffinityCache())
	require.Nil(t, (&GatewayService{}).ManagedModelAffinityCache())
	cache := &managedModelAffinityTestCache{}
	require.Same(t, cache, (&GatewayService{cache: cache}).ManagedModelAffinityCache())
	var openAI *OpenAIGatewayService
	binding, err := openAI.LookupManagedModelLegacyResponse(context.Background(), 9, "resp_old")
	require.NoError(t, err)
	require.Nil(t, binding)
	state := &managedModelAffinityTestLegacyStore{ownerKnown: true, ownerKey: 7, ownerUser: 3, accountID: 41}
	openAI = &OpenAIGatewayService{openaiWSStateStore: state}
	binding, err = openAI.LookupManagedModelLegacyResponse(context.Background(), 9, "resp_old")
	require.NoError(t, err)
	require.Equal(t, &ManagedModelLegacyResponseBinding{AccountID: 41, UserID: 3, APIKeyID: 7, OwnerKnown: true}, binding)
	state.ownerError = errors.New("owner cache unavailable")
	reads := state.accountReads
	binding, err = openAI.LookupManagedModelLegacyResponse(context.Background(), 9, "resp_old")
	require.ErrorIs(t, err, state.ownerError)
	require.Nil(t, binding)
	require.Equal(t, reads, state.accountReads)
	state.ownerError, state.ownerKnown, state.accountID = nil, false, 0
	binding, err = openAI.LookupManagedModelLegacyResponse(context.Background(), 9, "resp_old")
	require.NoError(t, err)
	require.Nil(t, binding)
}
