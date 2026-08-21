package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type duplicateReviewRequest struct {
	AccountIDs []int64 `json:"account_ids"`
}

type duplicateMergeRequest struct {
	SurvivorAccountID int64   `json:"survivor_account_id"`
	LoserAccountIDs   []int64 `json:"loser_account_ids"`
	ConfirmationHash  string  `json:"confirmation_hash"`
}

type duplicateReviewMetadata struct {
	ConfirmationHash string                       `json:"confirmation_hash"`
	Accounts         []duplicateReviewAccountMeta `json:"accounts"`
}

type duplicateReviewAccountMeta struct {
	AccountID          int64  `json:"account_id"`
	Name               string `json:"name"`
	GroupCount         int    `json:"group_count"`
	TagCount           int    `json:"tag_count"`
	ConfigurationScore int    `json:"configuration_score"`
}

func (h *AccountHandler) ReviewDuplicateAccounts(c *gin.Context) {
	var req duplicateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.AccountIDs = normalizeInt64IDList(req.AccountIDs)
	if len(req.AccountIDs) < 2 || len(req.AccountIDs) > service.AccountJobBatchSize {
		response.BadRequest(c, "account_ids must contain between 2 and 100 accounts")
		return
	}
	h.submitOneAccountJob(c, service.AccountJobKindDuplicateReview, req)
}

func (h *AccountHandler) MergeDuplicateAccounts(c *gin.Context) {
	var req duplicateMergeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.LoserAccountIDs = normalizeInt64IDList(req.LoserAccountIDs)
	if req.SurvivorAccountID <= 0 || len(req.LoserAccountIDs) == 0 || len(req.LoserAccountIDs) >= service.AccountJobBatchSize || strings.TrimSpace(req.ConfirmationHash) == "" {
		response.BadRequest(c, "survivor_account_id, loser_account_ids, and confirmation_hash are required")
		return
	}
	for _, id := range req.LoserAccountIDs {
		if id == req.SurvivorAccountID {
			response.BadRequest(c, "survivor account cannot also be a loser")
			return
		}
	}
	survivorID := req.SurvivorAccountID
	h.submitAccountJob(c, service.AccountJobKindDuplicateMerge, req, []service.AccountJobItemSeed{{
		Ordinal: 1, Action: "merge", TargetAccountID: &survivorID, Metadata: json.RawMessage(`{}`),
	}})
}

func (h *AccountHandler) duplicateReview(ctx context.Context, ids []int64) (*duplicateReviewMetadata, error) {
	ids = normalizeInt64IDList(ids)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	accounts, err := h.adminService.GetAccountsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*service.Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	metadata := &duplicateReviewMetadata{Accounts: make([]duplicateReviewAccountMeta, 0, len(ids))}
	canonical := struct {
		Fingerprint string   `json:"fingerprint"`
		Accounts    []string `json:"accounts"`
	}{}
	for _, id := range ids {
		account := byID[id]
		if account == nil {
			return nil, service.ErrAccountNotFound
		}
		if !service.IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			return nil, errors.New("duplicate review requires Cindy accounts")
		}
		normalizedURL, normalizeErr := service.NormalizeCredentialIdentityBaseURL(service.ProviderProfileCindyLaxaV1, account.GetCredential("base_url"))
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		fingerprint, fingerprintErr := service.AccountCredentialFingerprint(service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey, normalizedURL, account.GetCredential("api_key"))
		if fingerprintErr != nil {
			return nil, fingerprintErr
		}
		if canonical.Fingerprint == "" {
			canonical.Fingerprint = fingerprint
		} else if canonical.Fingerprint != fingerprint {
			return nil, service.ErrCredentialIdentityConflict
		}
		canonical.Accounts = append(canonical.Accounts, strings.Join([]string{
			strconvFormatInt(account.ID), account.UpdatedAt.UTC().Format(timeRFC3339Nano),
		}, ":"))
		score := len(account.GroupIDs) + len(account.Tags)
		if strings.TrimSpace(account.Name) != "" {
			score++
		}
		if account.Notes != nil && strings.TrimSpace(*account.Notes) != "" {
			score++
		}
		if account.ProxyID != nil {
			score++
		}
		metadata.Accounts = append(metadata.Accounts, duplicateReviewAccountMeta{
			AccountID: account.ID, Name: account.Name, GroupCount: len(account.GroupIDs),
			TagCount: len(account.Tags), ConfigurationScore: score,
		})
	}
	raw, _ := json.Marshal(canonical)
	digest := sha256.Sum256(raw)
	metadata.ConfirmationHash = hex.EncodeToString(digest[:])
	return metadata, nil
}

func strconvFormatInt(value int64) string { return strconv.FormatInt(value, 10) }

const timeRFC3339Nano = time.RFC3339Nano

func (h *AccountHandler) mergeDuplicateAccounts(ctx context.Context, req duplicateMergeRequest) (map[string]any, error) {
	ids := append([]int64{req.SurvivorAccountID}, req.LoserAccountIDs...)
	review, err := h.duplicateReview(ctx, ids)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(req.ConfirmationHash), review.ConfirmationHash) {
		return nil, errors.New("duplicate confirmation hash no longer matches")
	}
	accounts, err := h.adminService.GetAccountsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	var survivor *service.Account
	groupSet := map[int64]struct{}{}
	tagSet := map[int64]struct{}{}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		if account.ID == req.SurvivorAccountID {
			survivor = account
		}
		for _, groupID := range account.GroupIDs {
			groupSet[groupID] = struct{}{}
		}
		for _, tag := range account.Tags {
			tagSet[tag.ID] = struct{}{}
		}
	}
	if survivor == nil {
		return nil, service.ErrAccountNotFound
	}
	groupIDs := sortedInt64Set(groupSet)
	if _, err = h.adminService.UpdateAccount(ctx, survivor.ID, &service.UpdateAccountInput{GroupIDs: &groupIDs, SkipMixedChannelCheck: true}); err != nil {
		return nil, err
	}
	if console, consoleErr := h.accountConsoleService(); consoleErr == nil {
		if _, err = console.SetAccountTaxonomy(ctx, survivor.ID, service.AccountTaxonomyAssignment{
			FolderID: survivor.ManagementFolderID, TagIDs: sortedInt64Set(tagSet),
		}); err != nil {
			return nil, err
		}
	}
	for _, id := range req.LoserAccountIDs {
		if err = h.adminService.DeleteAccount(ctx, id); err != nil {
			return nil, err
		}
	}
	return map[string]any{"survivor_account_id": survivor.ID, "merged_count": len(req.LoserAccountIDs)}, nil
}

func sortedInt64Set(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		if value > 0 {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
