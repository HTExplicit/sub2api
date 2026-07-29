package admin

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	dataImportActionSkip   = "skip"
	dataImportActionUpdate = "update"
	dataImportActionCreate = "create"
)

type DataImportPreviewRequest struct {
	Data DataPayload `json:"data"`
}

type DataImportPreviewResult struct {
	Type     string                     `json:"type"`
	Version  int                        `json:"version"`
	Accounts []DataImportPreviewAccount `json:"accounts"`
	Proxies  []DataImportPreviewProxy   `json:"proxies"`
	Valid    bool                       `json:"valid"`
	Warnings []string                   `json:"warnings,omitempty"`
}

type DataImportPreviewMatch struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	MatchedBy string `json:"matched_by"`
}

type DataImportPreviewAccount struct {
	Index                 int                      `json:"index"`
	Name                  string                   `json:"name"`
	Platform              string                   `json:"platform"`
	Type                  string                   `json:"type"`
	MaskedEmail           string                   `json:"masked_email,omitempty"`
	Plan                  string                   `json:"plan,omitempty"`
	ManagementFolder      *string                  `json:"management_folder,omitempty"`
	Tags                  []string                 `json:"tags,omitempty"`
	Groups                []string                 `json:"groups,omitempty"`
	Valid                 bool                     `json:"valid"`
	Errors                []string                 `json:"errors,omitempty"`
	Warnings              []string                 `json:"warnings,omitempty"`
	StrongIdentityMatches []DataImportPreviewMatch `json:"strong_identity_matches,omitempty"`
	DuplicateOfIndex      *int                     `json:"duplicate_of_index,omitempty"`
	DefaultAction         string                   `json:"default_action"`
}

type DataImportPreviewProxy struct {
	Index         int      `json:"index"`
	Name          string   `json:"name"`
	Protocol      string   `json:"protocol"`
	Valid         bool     `json:"valid"`
	WillReuse     bool     `json:"will_reuse"`
	ExistingProxy *int64   `json:"existing_proxy_id,omitempty"`
	Errors        []string `json:"errors,omitempty"`
}

type dataIdentityKey struct {
	Value string
	Label string
}

type dataIdentityIndex struct {
	byKey map[string][]service.Account
}

type dataSeenImportIdentity struct {
	Index  int
	UserID string
}

func (h *AccountHandler) PreviewDataImport(c *gin.Context) {
	var req DataImportPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateDataHeader(req.Data); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.previewDataImport(c.Request.Context(), req.Data)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) previewDataImport(ctx context.Context, payload DataPayload) (*DataImportPreviewResult, error) {
	existingAccounts, err := h.listAccountsFiltered(ctx, "", "", "", "", 0, "", "name", "asc")
	if err != nil {
		return nil, err
	}
	existingProxies, err := h.listAllProxies(ctx)
	if err != nil {
		return nil, err
	}

	proxyIDs := make(map[string]int64, len(existingProxies))
	for _, proxy := range existingProxies {
		proxyIDs[buildProxyKey(proxy.Protocol, proxy.Host, proxy.Port, proxy.Username, proxy.Password)] = proxy.ID
	}

	result := &DataImportPreviewResult{
		Type: dataType, Version: normalizedDataPayloadVersion(payload), Valid: true,
		Accounts: make([]DataImportPreviewAccount, 0, len(payload.Accounts)),
		Proxies:  make([]DataImportPreviewProxy, 0, len(payload.Proxies)),
	}
	for index, proxy := range payload.Proxies {
		key := proxy.ProxyKey
		if key == "" {
			key = buildProxyKey(proxy.Protocol, proxy.Host, proxy.Port, proxy.Username, proxy.Password)
		}
		item := DataImportPreviewProxy{Index: index, Name: proxy.Name, Protocol: proxy.Protocol, Valid: true}
		if validateErr := validateDataProxy(proxy); validateErr != nil {
			item.Valid = false
			item.Errors = append(item.Errors, validateErr.Error())
			result.Valid = false
		}
		if proxyID, ok := proxyIDs[key]; ok {
			item.WillReuse = true
			item.ExistingProxy = &proxyID
		}
		result.Proxies = append(result.Proxies, item)
	}

	identityIndex := buildDataIdentityIndex(existingAccounts)
	existingNames := make(map[string][]service.Account)
	existingEmails := make(map[string][]service.Account)
	for _, account := range existingAccounts {
		if account.IsCredentialShadow() {
			continue
		}
		if name := normalizeDataWarningValue(account.Name); name != "" {
			existingNames[name] = append(existingNames[name], account)
		}
		if email := normalizeDataWarningValue(dataAccountEmail(account.Credentials, account.Extra)); email != "" {
			existingEmails[email] = append(existingEmails[email], account)
		}
	}

	seenImportKeys := make(map[string]dataSeenImportIdentity)
	for index := range payload.Accounts {
		account := payload.Accounts[index]
		enrichCredentialsFromIDToken(&account)
		email := dataAccountEmail(account.Credentials, account.Extra)
		preview := DataImportPreviewAccount{
			Index: index, Name: account.Name, Platform: account.Platform, Type: account.Type,
			MaskedEmail: maskDataEmail(email), Plan: dataAccountPlan(account.Credentials, account.Extra),
			ManagementFolder: account.ManagementFolder, Tags: account.Tags, Groups: account.Groups,
			Valid: true, DefaultAction: dataImportActionCreate,
		}
		if validateErr := validateDataAccountV2(account); validateErr != nil {
			preview.Valid = false
			preview.Errors = append(preview.Errors, validateErr.Error())
			preview.DefaultAction = dataImportActionSkip
			result.Valid = false
		}

		keys := dataAccountIdentityKeys(account.Platform, account.Credentials, account.Extra)
		preview.StrongIdentityMatches = identityIndex.Find(keys)
		if len(preview.StrongIdentityMatches) > 0 {
			preview.DefaultAction = dataImportActionSkip
		}
		incomingUserID := dataIdentityUserID(keys)
		for _, key := range keys {
			if firstSeen, ok := seenImportKeys[key.Value]; ok {
				if dataAccountIdentityUserConflicts(key.Label, incomingUserID, firstSeen.UserID) {
					continue
				}
				first := firstSeen.Index
				preview.DuplicateOfIndex = &first
				preview.DefaultAction = dataImportActionSkip
				break
			}
		}
		for _, key := range keys {
			if _, exists := seenImportKeys[key.Value]; !exists {
				seenImportKeys[key.Value] = dataSeenImportIdentity{Index: index, UserID: incomingUserID}
			}
		}

		if matches := existingNames[normalizeDataWarningValue(account.Name)]; len(matches) > 0 {
			preview.Warnings = append(preview.Warnings, fmt.Sprintf("%d existing account(s) use the same name", len(matches)))
		}
		if normalizedEmail := normalizeDataWarningValue(email); normalizedEmail != "" {
			if matches := existingEmails[normalizedEmail]; len(matches) > 0 {
				preview.Warnings = append(preview.Warnings, fmt.Sprintf("%d existing account(s) use the same email; email is not used for conflict matching", len(matches)))
			}
		}
		result.Accounts = append(result.Accounts, preview)
	}
	return result, nil
}

func normalizedDataPayloadVersion(payload DataPayload) int {
	if payload.Version == 0 {
		return dataVersionV1
	}
	return payload.Version
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

func normalizeDataWarningValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func maskDataEmail(value string) string {
	value = strings.TrimSpace(value)
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 {
		return ""
	}
	local := []rune(value[:at])
	if len(local) == 0 {
		return ""
	}
	return string(local[0]) + "***@" + value[at+1:]
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

func dataAccountEmail(credentials, extra map[string]any) string {
	if value := dataMapString(credentials, "email", "user_email"); value != "" {
		return value
	}
	return dataMapString(extra, "email", "user_email")
}

func dataAccountPlan(credentials, extra map[string]any) string {
	if value := dataMapString(credentials, "plan_type", "subscription_type", "tier_name", "tier_id"); value != "" {
		return value
	}
	return dataMapString(extra, "plan_type", "subscription_type", "tier_name", "tier_id")
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
	out := make([]dataIdentityKey, 0, 6)
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
		out = appendDataIdentityKey(out, seen, platform, "service_account_id", dataMapString(source, "service_account_id", "client_email"))
	}
	return out
}

func buildDataIdentityIndex(accounts []service.Account) *dataIdentityIndex {
	index := &dataIdentityIndex{byKey: make(map[string][]service.Account)}
	for _, account := range accounts {
		if account.IsCredentialShadow() {
			continue
		}
		for _, key := range dataAccountIdentityKeys(account.Platform, account.Credentials, account.Extra) {
			index.byKey[key.Value] = append(index.byKey[key.Value], account)
		}
	}
	return index
}

func (index *dataIdentityIndex) Find(keys []dataIdentityKey) []DataImportPreviewMatch {
	if index == nil {
		return nil
	}
	byID := make(map[int64]DataImportPreviewMatch)
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
				byID[account.ID] = DataImportPreviewMatch{AccountID: account.ID, Name: account.Name, MatchedBy: key.Label}
			}
		}
	}
	out := make([]DataImportPreviewMatch, 0, len(byID))
	for _, match := range byID {
		out = append(out, match)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}

func dataIdentityUserID(keys []dataIdentityKey) string {
	for _, key := range keys {
		if key.Label != "account_user_id" {
			continue
		}
		if separator := strings.LastIndexByte(key.Value, 0); separator >= 0 && separator+1 < len(key.Value) {
			return key.Value[separator+1:]
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
	if index == nil || account.IsCredentialShadow() {
		return
	}
	for _, key := range dataAccountIdentityKeys(account.Platform, account.Credentials, account.Extra) {
		items := index.byKey[key.Value]
		replaced := false
		for position := range items {
			if items[position].ID == account.ID {
				items[position] = account
				replaced = true
				break
			}
		}
		if !replaced {
			items = append(items, account)
		}
		index.byKey[key.Value] = items
	}
}
