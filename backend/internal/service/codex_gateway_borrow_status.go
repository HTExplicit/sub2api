package service

import (
	"context"
	"encoding/json"
	"fmt"
)

// Read local records without refreshing credentials or probing an upstream.
// The send path still checks the finalized wire identity immediately at dispatch.
func (s *CodexGatewayBorrowService) CurrentStatus(ctx context.Context) CodexGatewayBorrowStatus {
	status := s.Status()
	if s == nil || s.accounts == nil {
		return status
	}
	accounts := make(map[int64]*Account)
	for i := range status.Targets {
		row := &status.Targets[i]
		account, loaded := accounts[row.AccountID]
		if !loaded {
			account, _ = s.accounts.GetByID(ctx, row.AccountID)
			accounts[row.AccountID] = account
		}
		reason := ""
		if !codexGatewayBorrowAccountSupported(account) {
			reason = "account_unavailable"
		} else if account.Status != StatusActive {
			reason = "account_inactive"
		} else if !account.IsModelSupported(row.Model) {
			reason = "model_not_allowed"
		} else if normalizeOpenAIModelForUpstream(account, account.GetMappedModel(row.Model)) != row.Model {
			reason = "model_mapping_mismatch"
		} else if row.CacheValid {
			credential, err := resolveCredentialAccount(ctx, s.accounts, account)
			if err != nil || credential == nil {
				reason = "account_unavailable"
			} else {
				s.mu.Lock()
				check := s.targets[codexGatewayBorrowTargetKey{row.AccountID, row.Model}]
				s.mu.Unlock()
				if check.identityRevision != "" && check.identityRevision != borrowAccountIdentityRevision(account, credential) {
					reason = "identity_changed"
				}
			}
		}
		if reason != "" {
			row.CacheValid = false
			row.State = "waiting"
			row.Reason = reason
			row.Error = fmt.Sprintf("account %d: %s", row.AccountID, reason)
		}
	}
	return status
}

func borrowAccountIdentityRevision(account, credential *Account) string {
	if account == nil || credential == nil {
		return ""
	}
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	value, _ := json.Marshal([]any{credential.Credentials, credential.Extra["codex_client_identity"], credential.Extra["codex_fingerprint_seed"], account.Extra["codex_fingerprint_mode"], account.Credentials["model_mapping"], proxy, account.ProxyID})
	return borrowHash(string(value))
}
