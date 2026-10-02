package service

import "testing"

// The owner hash is stored in turn-state provenance, WS bridge ownership,
// continuation keys and quota estimates; these literals pin its formula.
func TestCodexCredentialOwnerIdentityIsStable(t *testing.T) {
	for want, account := range map[string]*Account{
		// sha256("openai\x00oauth\x00acc-1\x00user-1\x00org-1")
		"ca774e5584e7eba4c7f6866dcb6b5768461c31443d5057edf0428d80c020799e": {Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "rotates", "chatgpt_account_id": "acc-1", "chatgpt_user_id": "user-1", "organization_id": "org-1", "email": "ignored@example.com"}},
		// Without account and user ids the email joins: sha256("openai\x00setup-token\x00\x00\x00\x00owner@example.com")
		"b51584109ca9f089aa8eebb7c122ddbefcf3767642ce66dd9a99eb62881a5a57": {Platform: PlatformOpenAI, Type: AccountTypeSetupToken,
			Credentials: map[string]any{"email": "owner@example.com"}},
	} {
		if got := CodexCredentialOwnerIdentity(account); got != want {
			t.Fatalf("CodexCredentialOwnerIdentity(%s) = %s, want %s", account.Type, got, want)
		}
	}
	if CodexCredentialOwnerIdentity(nil) != "" {
		t.Fatal("a missing account has an owner identity")
	}
}
