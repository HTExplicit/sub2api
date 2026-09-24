package service

import "time"

// Native, non-secret scalar facts shared by the full and compact account DTOs.
type AccountViewFactsV1 struct {
	Version                  int    `json:"version"`
	Status                   string `json:"status"`
	Plan                     string `json:"plan"`
	PrivacyMode              string `json:"privacy_mode"`
	CanonicalCindy           bool   `json:"canonical_cindy"`
	CindyBalanceInsufficient bool   `json:"cindy_balance_insufficient"`
	CindyBanned              bool   `json:"cindy_banned"`
}

func AccountViewFactsFromAccount(account *Account, now time.Time) *AccountViewFactsV1 {
	if account == nil {
		return nil
	}
	privacy, _ := account.Extra["privacy_mode"].(string)
	return &AccountViewFactsV1{Version: 1, Status: accountConsoleStatus(account, now), Plan: accountConsolePlan(account), PrivacyMode: privacy, CanonicalCindy: accountViewCanonicalCindy(account), CindyBalanceInsufficient: account.CindyBalanceInsufficientAt != nil, CindyBanned: account.CindyBannedAt != nil}
}

func accountViewCanonicalCindy(account *Account) bool {
	return account != nil && account.Platform == PlatformCindy && account.WirePlatform == WirePlatformOpenAI && account.ProviderProfile == ProviderProfileCindyLaxaV1 && IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}
