//go:build unit

package service

func pelicanFixtureAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 4,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-chat-id"}, Extra: map[string]any{}}
}
