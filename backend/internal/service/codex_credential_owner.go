package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CodexCredentialOwnerIdentity identifies the principal behind an account's
// Codex credential. Token refresh changes access/refresh tokens, not the
// owner; a reauthorization to another principal changes it. Turn-state
// provenance, WS bridge ownership, continuation keys and quota estimates are
// bound to this value, so its formula must stay stable.
func CodexCredentialOwnerIdentity(a *Account) string {
	if a == nil {
		return ""
	}
	parts := []string{a.Platform, a.Type, a.GetCredential("chatgpt_account_id"), a.GetCredential("chatgpt_user_id"), a.GetCredential("organization_id")}
	if parts[2] == "" && parts[3] == "" {
		parts = append(parts, a.GetCredential("email"))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// isCodexCredentialOwner reports an OpenAI OAuth-like account that owns its
// credential. Credential shadows are excluded and keep their own forwarding
// policy.
func isCodexCredentialOwner(account *Account) bool {
	return account != nil && account.IsOpenAIOAuthLike() && !account.IsShadow()
}
