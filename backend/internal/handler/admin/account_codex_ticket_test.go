package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountResponseCodexTicketsDoesNotUseLegacySettings(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"legacy-model"}}
	h := &AccountHandler{cfg: cfg}
	for _, kind := range []string{service.AccountTypeOAuth, service.AccountTypeSetupToken} {
		account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: kind}
		require.Empty(t, h.accountResponseFromService(account).CodexTurnTickets)
		require.Empty(t, h.accountListResponseFromService(account).CodexTurnTickets)
	}
}
