package service

import "time"

// Native, non-secret scalar facts shared by the full and compact account DTOs.
type AccountViewFactsV1 struct {
	Version     int    `json:"version"`
	Status      string `json:"status"`
	Plan        string `json:"plan"`
	PrivacyMode string `json:"privacy_mode"`
}

func AccountViewFactsFromAccount(account *Account, now time.Time) *AccountViewFactsV1 {
	if account == nil {
		return nil
	}
	privacy, _ := account.Extra["privacy_mode"].(string)
	return &AccountViewFactsV1{Version: 1, Status: accountConsoleStatus(account, now), Plan: accountConsolePlan(account), PrivacyMode: privacy}
}
