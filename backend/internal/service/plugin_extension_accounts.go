package service

import (
	"context"
	"fmt"
	"net/http"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func extensionAccount(a *Account) *extensionv1.Account {
	if a == nil {
		return nil
	}
	return &extensionv1.Account{ID: a.ID, Platform: a.Platform, Type: a.Type, Status: a.Status, Shadow: a.IsShadow(),
		Identity: CodexTicketAccountIdentity(a), Revision: a.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func (s *OpenAIGatewayService) ReadExtensionAccount(ctx context.Context, id int64) (*extensionv1.Account, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return extensionAccount(a), nil
}

func (s *OpenAIGatewayService) ListExtensionAccounts(ctx context.Context, query extensionv1.AccountQuery) ([]extensionv1.Account, error) {
	statusFilter := StatusActive
	if query.IncludeInactive {
		statusFilter = ""
	}
	accounts, err := s.accountRepo.ListAllWithFilters(ctx, query.Platform, query.AccountType, statusFilter, "", 0, "")
	if err != nil {
		return nil, err
	}
	out := make([]extensionv1.Account, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if query.AccountType != "" && a.Type != query.AccountType {
			continue
		}
		if !query.IncludeInactive && !a.IsActive() {
			continue
		}
		out = append(out, *extensionAccount(a))
	}
	return out, nil
}

func (s *OpenAIGatewayService) ResolveExtensionIdentity(ctx context.Context, query extensionv1.AccountQuery) (*extensionv1.OutboundIdentity, error) {
	a, err := s.accountRepo.GetByID(ctx, query.AccountID)
	if err != nil {
		return nil, err
	}
	if a == nil || !a.IsOpenAIOAuthLike() || a.IsShadow() {
		return nil, fmt.Errorf("unsupported credential scope")
	}
	if query.PrepareCredentials {
		ctx = withCodexTicketCredentials(ctx)
	}
	token, _, err := s.GetAccessToken(ctx, a)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, a); err != nil {
		return nil, err
	}
	ensureCodexIdentityHeaders(headers)
	if err := enforceCodexIdentityHeadersForAccountContext(ctx, headers, a, codexAccountIdentityOverrideUA(a)); err != nil {
		return nil, err
	}
	return &extensionv1.OutboundIdentity{AccountID: a.ID, Identity: CodexTicketAccountIdentity(a), Token: token, Headers: headers, ProxyURL: resolveAccountProxyURL(a)}, nil
}
