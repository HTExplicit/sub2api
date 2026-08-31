package admin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	dataImportActionUpdate = "update"
	dataImportActionCreate = "create"
)

type DataImportIdentityMatch struct {
	AccountID int64
	Name      string
	MatchedBy string
}

type dataIdentityKey struct {
	Value string
	Label string
}

type dataIdentityIndex struct {
	byKey    map[string][]service.Account
	keysByID map[int64][]string
}

func validateDataAccountV2(item DataAccount) error {
	if err := validateDataAccount(item); err != nil {
		return err
	}
	if item.Status != "" {
		if _, err := normalizeDataAccountStatus(item.Status); err != nil {
			return err
		}
	}
	if item.ManagementFolder != nil {
		if err := validateDataTaxonomyName(*item.ManagementFolder, true); err != nil {
			return fmt.Errorf("management_folder: %w", err)
		}
	}
	for _, tag := range item.Tags {
		if err := validateDataTaxonomyName(tag, false); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
	}
	for _, group := range item.Groups {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("group name must not be empty")
		}
	}
	return nil
}

func validateDataTaxonomyName(value string, allowEmpty bool) error {
	value = strings.TrimSpace(value)
	if value == "" && allowEmpty {
		return nil
	}
	if value == "" || len([]rune(value)) > 100 {
		return fmt.Errorf("name must contain between 1 and 100 characters")
	}
	return nil
}

func normalizeDataAccountStatus(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", service.StatusActive:
		return strings.ToLower(strings.TrimSpace(value)), nil
	case "inactive", service.StatusDisabled:
		return service.StatusDisabled, nil
	case service.StatusError:
		return service.StatusError, nil
	default:
		return "", fmt.Errorf("account status is invalid: %s", value)
	}
}

func dataMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if normalized := strings.TrimSpace(typed); normalized != "" {
				return normalized
			}
		case fmt.Stringer:
			if normalized := strings.TrimSpace(typed.String()); normalized != "" {
				return normalized
			}
		case float64:
			if typed == float64(int64(typed)) {
				return strconv.FormatInt(int64(typed), 10)
			}
		}
	}
	return ""
}

func appendDataIdentityKey(out []dataIdentityKey, seen map[string]struct{}, platform, label, value string) []dataIdentityKey {
	value = strings.TrimSpace(value)
	if value == "" {
		return out
	}
	key := strings.ToLower(strings.TrimSpace(platform)) + "|" + label + "|" + value
	if _, ok := seen[key]; ok {
		return out
	}
	seen[key] = struct{}{}
	return append(out, dataIdentityKey{Value: key, Label: label})
}

func dataAccountIdentityKeys(platform string, credentials, extra map[string]any) []dataIdentityKey {
	out := make([]dataIdentityKey, 0, 7)
	seen := make(map[string]struct{})
	accountID := dataMapString(credentials, "chatgpt_account_id", "account_id", "xai_account_id", "gemini_account_id")
	userID := dataMapString(credentials, "chatgpt_user_id", "user_id", "claude_user_id", "anthropic_user_id", "xai_user_id", "google_user_id")
	if accountID == "" {
		accountID = dataMapString(extra, "account_id", "account_uuid", "xai_account_id", "gemini_account_id")
	}
	if userID == "" {
		userID = dataMapString(extra, "user_id", "user_uuid", "claude_user_id", "anthropic_user_id", "xai_user_id", "google_user_id")
	}
	if accountID != "" && userID != "" {
		out = appendDataIdentityKey(out, seen, platform, "account_user_id", accountID+"\x00"+userID)
	}
	if accountID != "" {
		out = appendDataIdentityKey(out, seen, platform, "account_id", accountID)
	} else {
		out = appendDataIdentityKey(out, seen, platform, "user_id", userID)
	}
	for _, source := range []map[string]any{credentials, extra} {
		out = appendDataIdentityKey(out, seen, platform, "provider_subject", dataMapString(source, "provider_subject", "subject"))
		out = appendDataIdentityKey(out, seen, platform, "crs_account_id", dataMapString(source, "crs_account_id"))
		out = appendDataIdentityKey(out, seen, platform, "service_account_id", dataMapString(source, "service_account_id"))
	}
	if service.IsCindyAPIKeyAccount(platform, service.AccountTypeAPIKey, credentials) {
		normalizedURL, err := service.NormalizeCredentialIdentityBaseURL(service.ProviderProfileCindyLaxaV1, dataMapString(credentials, "base_url"))
		if err == nil {
			apiKey, _ := credentials["api_key"].(string)
			fingerprint, fingerprintErr := service.AccountCredentialFingerprint(
				service.ProviderProfileCindyLaxaV1,
				service.AccountTypeAPIKey,
				normalizedURL,
				apiKey,
			)
			if fingerprintErr == nil {
				out = appendDataIdentityKey(out, seen, platform, "credential_fingerprint", fingerprint)
			}
		}
	}
	return out
}

func buildDataIdentityIndex(accounts []service.Account) *dataIdentityIndex {
	index := &dataIdentityIndex{
		byKey:    make(map[string][]service.Account),
		keysByID: make(map[int64][]string),
	}
	for _, account := range accounts {
		index.Add(account)
	}
	return index
}

func (index *dataIdentityIndex) Find(keys []dataIdentityKey) []DataImportIdentityMatch {
	if index == nil {
		return nil
	}
	byID := make(map[int64]DataImportIdentityMatch)
	incomingUserID := dataIdentityUserID(keys)
	for _, key := range keys {
		for _, account := range index.byKey[key.Value] {
			storedUserID := dataMapString(account.Credentials, "chatgpt_user_id", "user_id", "claude_user_id", "anthropic_user_id", "xai_user_id", "google_user_id")
			if storedUserID == "" {
				storedUserID = dataMapString(account.Extra, "user_id", "user_uuid", "claude_user_id", "anthropic_user_id", "xai_user_id", "google_user_id")
			}
			if dataAccountIdentityUserConflicts(key.Label, incomingUserID, storedUserID) {
				continue
			}
			if _, exists := byID[account.ID]; !exists {
				byID[account.ID] = DataImportIdentityMatch{AccountID: account.ID, Name: account.Name, MatchedBy: key.Label}
			}
		}
	}
	out := make([]DataImportIdentityMatch, 0, len(byID))
	for _, match := range byID {
		out = append(out, match)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}

func dataIdentityUserID(keys []dataIdentityKey) string {
	for _, key := range keys {
		if key.Label == "account_user_id" {
			if separator := strings.LastIndexByte(key.Value, 0); separator >= 0 && separator+1 < len(key.Value) {
				return key.Value[separator+1:]
			}
		}
	}
	for _, key := range keys {
		if key.Label == "user_id" {
			if separator := strings.LastIndexByte(key.Value, '|'); separator >= 0 && separator+1 < len(key.Value) {
				return key.Value[separator+1:]
			}
		}
	}
	return ""
}

func dataAccountIdentityUserConflicts(label, incomingUserID, storedUserID string) bool {
	if label != "account_id" {
		return false
	}
	incomingUserID = strings.TrimSpace(incomingUserID)
	storedUserID = strings.TrimSpace(storedUserID)
	return incomingUserID != "" && storedUserID != "" && incomingUserID != storedUserID
}

func (index *dataIdentityIndex) Add(account service.Account) {
	if index == nil {
		return
	}
	if index.byKey == nil {
		index.byKey = make(map[string][]service.Account)
	}
	if index.keysByID == nil {
		index.keysByID = make(map[int64][]string)
	}
	// Updated credentials can change every identity key. Remove the prior
	// reverse-indexed entries first so stale keys cannot match a later item.
	index.Remove(account.ID)
	if account.IsCredentialShadow() {
		return
	}
	for _, key := range dataAccountIdentityKeys(account.Platform, account.Credentials, account.Extra) {
		index.byKey[key.Value] = append(index.byKey[key.Value], account)
		index.keysByID[account.ID] = append(index.keysByID[account.ID], key.Value)
	}
}

func (index *dataIdentityIndex) Remove(accountID int64) {
	if index == nil || accountID <= 0 {
		return
	}
	for _, key := range index.keysByID[accountID] {
		items := index.byKey[key]
		kept := items[:0]
		for _, item := range items {
			if item.ID != accountID {
				kept = append(kept, item)
			}
		}
		if len(kept) == 0 {
			delete(index.byKey, key)
			continue
		}
		index.byKey[key] = kept
	}
	delete(index.keysByID, accountID)
}
