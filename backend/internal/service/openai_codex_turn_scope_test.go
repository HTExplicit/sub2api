package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSCodexTurnScopeRetainsFirstValueAndRejectsOtherOwners(t *testing.T) {
	service := &OpenAIGatewayService{}
	store := NewOpenAIWSStateStore(nil)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	c, _ := newTurnStateTestContext(t, 11, "session")
	firstScope := openAIWSTurnStateScope(c, account, "execution")
	service.commitOpenAIWSSessionTurnState(c, account, store, 1, "execution", "first")
	service.commitOpenAIWSSessionTurnState(c, account, store, 1, "execution", "later")
	service.commitOpenAIWSSessionTurnState(c, account, store, 1, "execution", "")
	state, ok := store.GetSessionTurnState(1, firstScope, account.ID)
	require.True(t, ok)
	require.Equal(t, "first", state)
	other := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	_, ok = store.GetSessionTurnState(1, firstScope, other.ID)
	require.False(t, ok)
	foreign := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"first"}}
	service.guardOpenAICodexTurnStateEcho(c, other, foreign)
	require.Empty(t, foreign.Get(openAICodexTurnStateHeader))
	stageCodexRoutingTurn(c, []byte(`{"client_metadata":{"turn_id":"next-turn"}}`))
	nextScope := openAIWSTurnStateScope(c, account, "execution")
	require.NotEqual(t, firstScope, nextScope)
	_, ok = store.GetSessionTurnState(1, nextScope, account.ID)
	require.False(t, ok)
	prior := http.Header{http.CanonicalHeaderKey(openAICodexTurnStateHeader): []string{"first"}}
	service.guardOpenAICodexTurnStateEcho(c, account, prior)
	require.Empty(t, prior.Get(openAICodexTurnStateHeader))
	c.Request.Header.Del(openAIWSTurnMetadataHeader)
	stageCodexRoutingTurn(c, nil)
	require.Empty(t, openAIWSTurnStateScope(c, account, "execution"))
	apiKey := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.Equal(t, "execution", openAIWSTurnStateScope(c, apiKey, "execution"))
	require.Equal(t, "11\x00session", openAICodexTurnStateSeed(c, apiKey))
}
