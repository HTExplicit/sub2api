package service

import (
	"context"
)

// Ticket preparation can refresh credentials without enrolling the account in
// business scheduling or replacing an administrator's status/error decision.
type codexTicketCredentialContextKey struct{}

func withCodexTicketCredentials(ctx context.Context) context.Context {
	return context.WithValue(ctx, codexTicketCredentialContextKey{}, true)
}

func isCodexTicketCredentialContext(ctx context.Context) bool {
	ticket, _ := ctx.Value(codexTicketCredentialContextKey{}).(bool)
	return ticket
}

func (s *OpenAIGatewayService) CodexTicketModels() []string {
	return append([]string(nil), s.openAICodexTicketConfig().Models...)
}
func (s *OpenAIGatewayService) CodexTicketsEnabled(ctx context.Context) bool {
	return s.openAICodexTicketEnabledContext(ctx)
}
