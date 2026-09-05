package service

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbaccountfolder "github.com/Wei-Shaw/sub2api/ent/accountfolder"
	dbaccountgroup "github.com/Wei-Shaw/sub2api/ent/accountgroup"
	dbaccounttag "github.com/Wei-Shaw/sub2api/ent/accounttag"
	dbaccounttagbinding "github.com/Wei-Shaw/sub2api/ent/accounttagbinding"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
)

var (
	ErrAccountFolderNotFound = infraerrors.NotFound("ACCOUNT_FOLDER_NOT_FOUND", "account folder not found")
	ErrAccountTagNotFound    = infraerrors.NotFound("ACCOUNT_TAG_NOT_FOUND", "account tag not found")
	ErrAccountFolderExists   = infraerrors.Conflict("ACCOUNT_FOLDER_EXISTS", "an account folder with this name already exists")
	ErrAccountTagExists      = infraerrors.Conflict("ACCOUNT_TAG_EXISTS", "an account tag with this name already exists")
)

type AccountTaxonomyInput struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type AccountTaxonomyAssignment struct {
	FolderID *int64  `json:"folder_id"`
	TagIDs   []int64 `json:"tag_ids"`
}

type BulkAccountTaxonomyInput struct {
	AccountIDs         []int64
	Filters            *BulkUpdateAccountFilters
	ExpectedMatchCount *int
	FolderAction       string
	FolderID           *int64
	TagAddIDs          []int64
	TagRemoveIDs       []int64
}

type BulkAccountTaxonomyResult struct {
	MatchedCount int `json:"matched_count"`
	UpdatedCount int `json:"updated_count"`
}

type AccountConsoleFilters struct {
	Platforms            []string `json:"platforms,omitempty"`
	Types                []string `json:"types,omitempty"`
	Statuses             []string `json:"statuses,omitempty"`
	Plans                []string `json:"plans,omitempty"`
	ProxyIDs             []int64  `json:"proxy_ids,omitempty"`
	IncludeDirect        bool     `json:"include_direct,omitempty"`
	FolderIDs            []int64  `json:"folder_ids,omitempty"`
	IncludeUncategorized bool     `json:"include_uncategorized,omitempty"`
	TagIDs               []int64  `json:"tag_ids,omitempty"`
	AccountIDs           []int64  `json:"account_ids,omitempty"`
	Search               string   `json:"search,omitempty"`
	GroupID              int64    `json:"group_id,omitempty"`
	PrivacyMode          string   `json:"privacy_mode,omitempty"`
	CindyOnly            bool     `json:"cindy_only,omitempty"`
	CindyBalanceStatus   string   `json:"cindy_balance_status,omitempty"`
	CindyHealthStatus    string   `json:"cindy_health_status,omitempty"`
	SortBy               string   `json:"sort_by,omitempty"`
	SortOrder            string   `json:"sort_order,omitempty"`
}

type AccountFacetOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type AccountConsoleFacets struct {
	Total              int                       `json:"total"`
	UncategorizedCount int                       `json:"uncategorized_count"`
	Platforms          []AccountFacetOption      `json:"platforms"`
	Types              []AccountFacetOption      `json:"types"`
	Statuses           []AccountFacetOption      `json:"statuses"`
	Plans              []AccountFacetOption      `json:"plans"`
	Proxies            []AccountFacetOption      `json:"proxies"`
	Folders            []AccountManagementFolder `json:"folders"`
	Tags               []AccountManagementTag    `json:"tags"`
	CindyTotal         int                       `json:"cindy_total"`
	CindyInsufficient  int                       `json:"cindy_insufficient_count"`
	CindyBanned        int                       `json:"cindy_banned_count"`
}

func normalizeAccountTaxonomyName(value string) (string, string, error) {
	display := strings.TrimSpace(value)
	if display == "" || len([]rune(display)) > 100 {
		return "", "", infraerrors.BadRequest("ACCOUNT_TAXONOMY_NAME_INVALID", "name must contain between 1 and 100 characters")
	}
	return display, strings.ToLower(display), nil
}

func (s *adminServiceImpl) ListAccountFolders(ctx context.Context) ([]AccountManagementFolder, error) {
	return s.listAccountFolders(ctx, true)
}

func (s *adminServiceImpl) listAccountFolders(ctx context.Context, includeCounts bool) ([]AccountManagementFolder, error) {
	client := s.entClient
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		client = contextTx.Client()
	}
	rows, err := client.AccountFolder.Query().
		Order(dbent.Asc(dbaccountfolder.FieldSortOrder), dbent.Asc(dbaccountfolder.FieldName)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int)
	if includeCounts {
		var grouped []struct {
			ManagementFolderID int64 `json:"management_folder_id"`
			Count              int   `json:"count"`
		}
		if err := client.Account.Query().
			Where(dbaccount.ManagementFolderIDNotNil()).
			GroupBy(dbaccount.FieldManagementFolderID).
			Aggregate(dbent.Count()).
			Scan(ctx, &grouped); err != nil {
			return nil, err
		}
		for _, item := range grouped {
			counts[item.ManagementFolderID] = item.Count
		}
	}
	out := make([]AccountManagementFolder, 0, len(rows))
	for _, row := range rows {
		out = append(out, AccountManagementFolder{
			ID: row.ID, Name: row.Name, SortOrder: row.SortOrder, AccountCount: counts[row.ID],
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

func (s *adminServiceImpl) CreateAccountFolder(ctx context.Context, input AccountTaxonomyInput) (*AccountManagementFolder, error) {
	name, normalized, err := normalizeAccountTaxonomyName(input.Name)
	if err != nil {
		return nil, err
	}
	client := s.entClient
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		client = contextTx.Client()
	}
	exists, err := client.AccountFolder.Query().Where(dbaccountfolder.NormalizedNameEQ(normalized)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrAccountFolderExists
	}
	row, err := client.AccountFolder.Create().
		SetName(name).SetNormalizedName(normalized).SetSortOrder(input.SortOrder).Save(ctx)
	if err != nil {
		if dbent.IsConstraintError(err) {
			return nil, ErrAccountFolderExists
		}
		return nil, err
	}
	return &AccountManagementFolder{ID: row.ID, Name: row.Name, SortOrder: row.SortOrder, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (s *adminServiceImpl) UpdateAccountFolder(ctx context.Context, id int64, input AccountTaxonomyInput) (*AccountManagementFolder, error) {
	name, normalized, err := normalizeAccountTaxonomyName(input.Name)
	if err != nil {
		return nil, err
	}
	row, err := s.entClient.AccountFolder.UpdateOneID(id).
		SetName(name).SetNormalizedName(normalized).SetSortOrder(input.SortOrder).Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrAccountFolderNotFound
		}
		if dbent.IsConstraintError(err) {
			return nil, ErrAccountFolderExists
		}
		return nil, err
	}
	count, err := row.QueryAccounts().Count(ctx)
	if err != nil {
		return nil, err
	}
	return &AccountManagementFolder{ID: row.ID, Name: row.Name, SortOrder: row.SortOrder, AccountCount: count, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (s *adminServiceImpl) DeleteAccountFolder(ctx context.Context, id int64, moveAccounts bool) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.AccountFolder.Query().
		Where(dbaccountfolder.IDEQ(id)).
		ForUpdate().
		Only(ctx); err != nil {
		if dbent.IsNotFound(err) {
			return ErrAccountFolderNotFound
		}
		return err
	}
	count, err := tx.Account.Query().Where(dbaccount.ManagementFolderIDEQ(id)).Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 && !moveAccounts {
		return infraerrors.Conflict("ACCOUNT_FOLDER_NOT_EMPTY", "folder contains accounts; confirm moving them to uncategorized")
	}
	// Taxonomy is presentation-only. Hidden soft-deleted accounts must not keep
	// a RESTRICT foreign key that makes an otherwise empty folder undeletable.
	if _, err = tx.Account.Update().
		Where(dbaccount.ManagementFolderIDEQ(id)).
		ClearManagementFolderID().
		Save(mixins.SkipSoftDelete(ctx)); err != nil {
		return err
	}
	if err = tx.AccountFolder.DeleteOneID(id).Exec(ctx); err != nil {
		if dbent.IsNotFound(err) {
			return ErrAccountFolderNotFound
		}
		if dbent.IsConstraintError(err) {
			return infraerrors.Conflict("ACCOUNT_FOLDER_NOT_EMPTY", "folder references changed; reload and retry")
		}
		return err
	}
	return tx.Commit()
}

func (s *adminServiceImpl) ListAccountTags(ctx context.Context) ([]AccountManagementTag, error) {
	return s.listAccountTags(ctx, true)
}

func (s *adminServiceImpl) listAccountTags(ctx context.Context, includeCounts bool) ([]AccountManagementTag, error) {
	client := s.entClient
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		client = contextTx.Client()
	}
	rows, err := client.AccountTag.Query().
		Order(dbent.Asc(dbaccounttag.FieldSortOrder), dbent.Asc(dbaccounttag.FieldName)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int)
	if includeCounts {
		var grouped []struct {
			TagID int64 `json:"tag_id"`
			Count int   `json:"count"`
		}
		if err := client.AccountTagBinding.Query().
			Where(dbaccounttagbinding.HasAccountWith(dbaccount.DeletedAtIsNil())).
			GroupBy(dbaccounttagbinding.FieldTagID).
			Aggregate(dbent.Count()).
			Scan(ctx, &grouped); err != nil {
			return nil, err
		}
		for _, item := range grouped {
			counts[item.TagID] = item.Count
		}
	}
	out := make([]AccountManagementTag, 0, len(rows))
	for _, row := range rows {
		out = append(out, AccountManagementTag{
			ID: row.ID, Name: row.Name, SortOrder: row.SortOrder, AccountCount: counts[row.ID],
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

func (s *adminServiceImpl) CreateAccountTag(ctx context.Context, input AccountTaxonomyInput) (*AccountManagementTag, error) {
	name, normalized, err := normalizeAccountTaxonomyName(input.Name)
	if err != nil {
		return nil, err
	}
	client := s.entClient
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		client = contextTx.Client()
	}
	exists, err := client.AccountTag.Query().Where(dbaccounttag.NormalizedNameEQ(normalized)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrAccountTagExists
	}
	row, err := client.AccountTag.Create().
		SetName(name).SetNormalizedName(normalized).SetSortOrder(input.SortOrder).Save(ctx)
	if err != nil {
		if dbent.IsConstraintError(err) {
			return nil, ErrAccountTagExists
		}
		return nil, err
	}
	return &AccountManagementTag{ID: row.ID, Name: row.Name, SortOrder: row.SortOrder, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (s *adminServiceImpl) UpdateAccountTag(ctx context.Context, id int64, input AccountTaxonomyInput) (*AccountManagementTag, error) {
	name, normalized, err := normalizeAccountTaxonomyName(input.Name)
	if err != nil {
		return nil, err
	}
	row, err := s.entClient.AccountTag.UpdateOneID(id).
		SetName(name).SetNormalizedName(normalized).SetSortOrder(input.SortOrder).Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrAccountTagNotFound
		}
		if dbent.IsConstraintError(err) {
			return nil, ErrAccountTagExists
		}
		return nil, err
	}
	count, err := row.QueryAccounts().Count(ctx)
	if err != nil {
		return nil, err
	}
	return &AccountManagementTag{ID: row.ID, Name: row.Name, SortOrder: row.SortOrder, AccountCount: count, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (s *adminServiceImpl) DeleteAccountTag(ctx context.Context, id int64) error {
	err := s.entClient.AccountTag.DeleteOneID(id).Exec(ctx)
	if dbent.IsNotFound(err) {
		return ErrAccountTagNotFound
	}
	return err
}

func validateTaxonomyOrderIDs(actual, ordered []int64) error {
	if len(actual) != len(ordered) {
		return infraerrors.Conflict("ACCOUNT_TAXONOMY_ORDER_CHANGED", "account taxonomy changed; reload and try again")
	}
	seen := make(map[int64]struct{}, len(ordered))
	for _, id := range ordered {
		if id <= 0 {
			return infraerrors.BadRequest("ACCOUNT_TAXONOMY_ORDER_INVALID", "ordered_ids must contain positive unique IDs")
		}
		if _, exists := seen[id]; exists {
			return infraerrors.BadRequest("ACCOUNT_TAXONOMY_ORDER_INVALID", "ordered_ids must contain positive unique IDs")
		}
		seen[id] = struct{}{}
	}
	for _, id := range actual {
		if _, exists := seen[id]; !exists {
			return infraerrors.Conflict("ACCOUNT_TAXONOMY_ORDER_CHANGED", "account taxonomy changed; reload and try again")
		}
	}
	return nil
}

func (s *adminServiceImpl) ReorderAccountFolders(ctx context.Context, orderedIDs []int64) ([]AccountManagementFolder, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.AccountFolder.Query().ForUpdate().All(ctx)
	if err != nil {
		return nil, err
	}
	actual := make([]int64, 0, len(rows))
	for _, row := range rows {
		actual = append(actual, row.ID)
	}
	if err = validateTaxonomyOrderIDs(actual, orderedIDs); err != nil {
		return nil, err
	}
	for index, id := range orderedIDs {
		if _, err = tx.AccountFolder.UpdateOneID(id).SetSortOrder(index).Save(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListAccountFolders(ctx)
}

func (s *adminServiceImpl) ReorderAccountTags(ctx context.Context, orderedIDs []int64) ([]AccountManagementTag, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.AccountTag.Query().ForUpdate().All(ctx)
	if err != nil {
		return nil, err
	}
	actual := make([]int64, 0, len(rows))
	for _, row := range rows {
		actual = append(actual, row.ID)
	}
	if err = validateTaxonomyOrderIDs(actual, orderedIDs); err != nil {
		return nil, err
	}
	for index, id := range orderedIDs {
		if _, err = tx.AccountTag.UpdateOneID(id).SetSortOrder(index).Save(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListAccountTags(ctx)
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func strictUniquePositiveIDs(ids []int64, reason string) ([]int64, error) {
	unique := uniquePositiveIDs(ids)
	if len(unique) != len(ids) {
		return nil, infraerrors.BadRequest(reason, "IDs must be positive and unique")
	}
	return unique, nil
}

func taxonomyIDsOverlap(left, right []int64) bool {
	seen := make(map[int64]struct{}, len(left))
	for _, id := range left {
		seen[id] = struct{}{}
	}
	for _, id := range right {
		if _, exists := seen[id]; exists {
			return true
		}
	}
	return false
}

func (s *adminServiceImpl) BulkUpdateAccountTaxonomy(ctx context.Context, input BulkAccountTaxonomyInput) (*BulkAccountTaxonomyResult, error) {
	selectedTarget := len(input.AccountIDs) > 0
	filteredTarget := input.Filters != nil
	if selectedTarget == filteredTarget {
		return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_TARGET_INVALID", "provide exactly one of account_ids or filters")
	}
	var err error
	input.AccountIDs, err = strictUniquePositiveIDs(input.AccountIDs, "ACCOUNT_TAXONOMY_ACCOUNT_IDS_INVALID")
	if err != nil {
		return nil, err
	}
	input.TagAddIDs, err = strictUniquePositiveIDs(input.TagAddIDs, "ACCOUNT_TAXONOMY_TAG_IDS_INVALID")
	if err != nil {
		return nil, err
	}
	input.TagRemoveIDs, err = strictUniquePositiveIDs(input.TagRemoveIDs, "ACCOUNT_TAXONOMY_TAG_IDS_INVALID")
	if err != nil {
		return nil, err
	}
	if taxonomyIDsOverlap(input.TagAddIDs, input.TagRemoveIDs) {
		return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_TAG_OPERATION_CONFLICT", "a tag cannot be added and removed in the same request")
	}
	switch input.FolderAction {
	case "":
		if input.FolderID != nil {
			return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_FOLDER_ACTION_INVALID", "folder_id requires folder_action=set")
		}
	case "set":
		if input.FolderID == nil || *input.FolderID <= 0 {
			return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_FOLDER_ID_INVALID", "folder_id must be positive when folder_action=set")
		}
	case "clear":
		if input.FolderID != nil {
			return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_FOLDER_ACTION_INVALID", "folder_id must be omitted when folder_action=clear")
		}
	default:
		return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_FOLDER_ACTION_INVALID", "folder_action must be set, clear, or omitted")
	}
	if input.FolderAction == "" && len(input.TagAddIDs) == 0 && len(input.TagRemoveIDs) == 0 {
		return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_OPERATION_REQUIRED", "at least one taxonomy operation is required")
	}
	if filteredTarget {
		if input.ExpectedMatchCount == nil || *input.ExpectedMatchCount < 0 {
			return nil, infraerrors.BadRequest("ACCOUNT_TAXONOMY_EXPECTED_COUNT_REQUIRED", "expected_match_count is required for filter targets")
		}
		input.AccountIDs, err = s.resolveBulkUpdateTargetIDs(ctx, input.Filters)
		if err != nil {
			return nil, err
		}
		input.AccountIDs = uniquePositiveIDs(input.AccountIDs)
		if len(input.AccountIDs) != *input.ExpectedMatchCount {
			return nil, infraerrors.Conflict("ACCOUNT_TAXONOMY_TARGET_CHANGED", "matching accounts changed; reload and confirm again").WithMetadata(map[string]string{
				"expected_match_count": strconv.Itoa(*input.ExpectedMatchCount),
				"actual_match_count":   strconv.Itoa(len(input.AccountIDs)),
			})
		}
	}
	if len(input.AccountIDs) == 0 {
		return &BulkAccountTaxonomyResult{}, nil
	}

	contextTx := dbent.TxFromContext(ctx)
	var txClient *dbent.Client
	var ownedTx *dbent.Tx
	if contextTx != nil {
		txClient = contextTx.Client()
	} else {
		ownedTx, err = s.entClient.Tx(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = ownedTx.Rollback() }()
		txClient = ownedTx.Client()
	}
	lockedAccounts, err := txClient.Account.Query().Where(
		dbaccount.IDIn(input.AccountIDs...),
		dbaccount.DeletedAtIsNil(),
	).ForUpdate().IDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(lockedAccounts) != len(input.AccountIDs) {
		if filteredTarget {
			return nil, infraerrors.Conflict("ACCOUNT_TAXONOMY_TARGET_CHANGED", "matching accounts changed; reload and confirm again")
		}
		return nil, ErrAccountNotFound
	}
	if input.FolderAction == "set" {
		exists, queryErr := txClient.AccountFolder.Query().Where(dbaccountfolder.IDEQ(*input.FolderID)).Exist(ctx)
		if queryErr != nil {
			return nil, queryErr
		}
		if !exists {
			return nil, ErrAccountFolderNotFound
		}
	}
	allTagIDs := append(append([]int64(nil), input.TagAddIDs...), input.TagRemoveIDs...)
	if len(allTagIDs) > 0 {
		count, queryErr := txClient.AccountTag.Query().Where(dbaccounttag.IDIn(allTagIDs...)).Count(ctx)
		if queryErr != nil {
			return nil, queryErr
		}
		if count != len(allTagIDs) {
			return nil, ErrAccountTagNotFound
		}
	}
	if input.FolderAction != "" {
		update := txClient.Account.Update().Where(dbaccount.IDIn(input.AccountIDs...), dbaccount.DeletedAtIsNil())
		if input.FolderAction == "set" {
			update.SetManagementFolderID(*input.FolderID)
		} else {
			update.ClearManagementFolderID()
		}
		if _, err = update.Save(ctx); err != nil {
			return nil, err
		}
	}
	if len(input.TagRemoveIDs) > 0 {
		if _, err = txClient.AccountTagBinding.Delete().Where(
			dbaccounttagbinding.AccountIDIn(input.AccountIDs...),
			dbaccounttagbinding.TagIDIn(input.TagRemoveIDs...),
		).Exec(ctx); err != nil {
			return nil, err
		}
	}
	if len(input.TagAddIDs) > 0 {
		const bindingBatchSize = 2000
		builders := make([]*dbent.AccountTagBindingCreate, 0, len(input.AccountIDs)*len(input.TagAddIDs))
		for _, accountID := range input.AccountIDs {
			for _, tagID := range input.TagAddIDs {
				builders = append(builders, txClient.AccountTagBinding.Create().SetAccountID(accountID).SetTagID(tagID))
				if len(builders) == bindingBatchSize {
					if err = txClient.AccountTagBinding.CreateBulk(builders...).OnConflictColumns("account_id", "tag_id").DoNothing().Exec(ctx); err != nil {
						return nil, err
					}
					builders = builders[:0]
				}
			}
		}
		if len(builders) > 0 {
			if err = txClient.AccountTagBinding.CreateBulk(builders...).OnConflictColumns("account_id", "tag_id").DoNothing().Exec(ctx); err != nil {
				return nil, err
			}
		}
	}
	if ownedTx != nil {
		if err = ownedTx.Commit(); err != nil {
			return nil, err
		}
	}
	return &BulkAccountTaxonomyResult{MatchedCount: len(input.AccountIDs), UpdatedCount: len(input.AccountIDs)}, nil
}

const accountTaxonomyHydrationBatchSize = 10000

func (s *adminServiceImpl) hydrateAccountTaxonomyValues(ctx context.Context, accounts []Account) error {
	pointers := make([]*Account, 0, len(accounts))
	for i := range accounts {
		pointers = append(pointers, &accounts[i])
	}
	return s.hydrateAccountTaxonomy(ctx, pointers)
}

func (s *adminServiceImpl) hydrateAccountTaxonomy(ctx context.Context, accounts []*Account) error {
	if s == nil || s.entClient == nil || len(accounts) == 0 {
		return nil
	}
	byID := make(map[int64]*Account, len(accounts))
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account == nil || account.ID <= 0 {
			continue
		}
		account.ManagementFolder = nil
		account.Tags = nil
		if _, exists := byID[account.ID]; exists {
			continue
		}
		byID[account.ID] = account
		ids = append(ids, account.ID)
	}
	client := s.entClient
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		client = contextTx.Client()
	}
	for start := 0; start < len(ids); start += accountTaxonomyHydrationBatchSize {
		end := start + accountTaxonomyHydrationBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		rows, err := client.Account.Query().
			Where(dbaccount.IDIn(ids[start:end]...)).
			WithManagementFolder().
			WithTags().
			All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			account := byID[row.ID]
			if account == nil {
				continue
			}
			if folder := row.Edges.ManagementFolder; folder != nil {
				account.ManagementFolder = &AccountManagementFolder{
					ID: folder.ID, Name: folder.Name, SortOrder: folder.SortOrder,
					CreatedAt: folder.CreatedAt, UpdatedAt: folder.UpdatedAt,
				}
			}
			for _, tag := range row.Edges.Tags {
				account.Tags = append(account.Tags, AccountManagementTag{
					ID: tag.ID, Name: tag.Name, SortOrder: tag.SortOrder,
					CreatedAt: tag.CreatedAt, UpdatedAt: tag.UpdatedAt,
				})
			}
			sort.SliceStable(account.Tags, func(i, j int) bool {
				left, right := account.Tags[i], account.Tags[j]
				if left.SortOrder != right.SortOrder {
					return left.SortOrder < right.SortOrder
				}
				return strings.ToLower(left.Name) < strings.ToLower(right.Name)
			})
		}
	}
	return nil
}

func (s *adminServiceImpl) SetAccountTaxonomy(ctx context.Context, accountID int64, assignment AccountTaxonomyAssignment) (*Account, error) {
	assignment.TagIDs = uniquePositiveIDs(assignment.TagIDs)
	contextTx := dbent.TxFromContext(ctx)
	var txClient *dbent.Client
	var ownedTx *dbent.Tx
	var err error
	if contextTx != nil {
		txClient = contextTx.Client()
	} else {
		ownedTx, err = s.entClient.Tx(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = ownedTx.Rollback() }()
		txClient = ownedTx.Client()
	}
	if exists, queryErr := txClient.Account.Query().Where(dbaccount.IDEQ(accountID)).Exist(ctx); queryErr != nil {
		return nil, queryErr
	} else if !exists {
		return nil, ErrAccountNotFound
	}
	if assignment.FolderID != nil {
		if *assignment.FolderID <= 0 {
			return nil, infraerrors.BadRequest("ACCOUNT_FOLDER_ID_INVALID", "folder_id must be positive or null")
		}
		if exists, queryErr := txClient.AccountFolder.Query().Where(dbaccountfolder.IDEQ(*assignment.FolderID)).Exist(ctx); queryErr != nil {
			return nil, queryErr
		} else if !exists {
			return nil, ErrAccountFolderNotFound
		}
	}
	if len(assignment.TagIDs) > 0 {
		count, queryErr := txClient.AccountTag.Query().Where(dbaccounttag.IDIn(assignment.TagIDs...)).Count(ctx)
		if queryErr != nil {
			return nil, queryErr
		}
		if count != len(assignment.TagIDs) {
			return nil, ErrAccountTagNotFound
		}
	}
	update := txClient.Account.UpdateOneID(accountID).ClearTags()
	if assignment.FolderID == nil {
		update.ClearManagementFolderID()
	} else {
		update.SetManagementFolderID(*assignment.FolderID)
	}
	if len(assignment.TagIDs) > 0 {
		update.AddTagIDs(assignment.TagIDs...)
	}
	if _, err = update.Save(ctx); err != nil {
		return nil, err
	}
	if ownedTx != nil {
		if err = ownedTx.Commit(); err != nil {
			return nil, err
		}
	}
	return s.GetAccount(ctx, accountID)
}

func (s *adminServiceImpl) accountConsoleQuery(filters AccountConsoleFilters) *dbent.AccountQuery {
	query := s.entClient.Account.Query()
	if filters.CindyOnly {
		query = query.Where(
			dbaccount.PlatformEQ(PlatformCindy),
			dbaccount.WirePlatformEQ(WirePlatformOpenAI),
			dbaccount.ProviderProfileEQ(ProviderProfileCindyLaxaV1),
			dbaccount.TypeEQ(AccountTypeAPIKey),
		)
	}
	if filters.CindyBalanceStatus == "insufficient" {
		query = query.Where(dbaccount.CindyBalanceInsufficientAtNotNil())
	}
	if filters.CindyHealthStatus == "banned" {
		query = query.Where(dbaccount.CindyBannedAtNotNil())
	}
	if values := normalizeStringFilter(filters.Platforms); len(values) > 0 {
		query = query.Where(dbaccount.PlatformIn(values...))
	}
	if values := normalizeStringFilter(filters.Types); len(values) > 0 {
		query = query.Where(dbaccount.TypeIn(values...))
	}
	if search := strings.TrimSpace(filters.Search); search != "" {
		query = query.Where(dbaccount.NameContainsFold(search))
	}
	if len(filters.AccountIDs) > 0 {
		query = query.Where(dbaccount.IDIn(uniquePositiveIDs(filters.AccountIDs)...))
	}
	if filters.GroupID == AccountListGroupUngrouped {
		query = query.Where(dbaccount.Not(dbaccount.HasAccountGroups()))
	} else if filters.GroupID > 0 {
		query = query.Where(dbaccount.HasAccountGroupsWith(dbaccountgroup.GroupIDEQ(filters.GroupID)))
	}
	if filters.PrivacyMode != "" {
		query = query.Where(func(selector *entsql.Selector) {
			path := sqljson.Path("privacy_mode")
			if filters.PrivacyMode == AccountPrivacyModeUnsetFilter {
				selector.Where(entsql.Or(
					entsql.Not(sqljson.HasKey(dbaccount.FieldExtra, path)),
					sqljson.ValueEQ(dbaccount.FieldExtra, "", path),
				))
				return
			}
			selector.Where(sqljson.ValueEQ(dbaccount.FieldExtra, filters.PrivacyMode, path))
		})
	}

	folderIDs := uniquePositiveIDs(filters.FolderIDs)
	if len(folderIDs) > 0 && filters.IncludeUncategorized {
		query = query.Where(dbaccount.Or(dbaccount.ManagementFolderIDIn(folderIDs...), dbaccount.ManagementFolderIDIsNil()))
	} else if len(folderIDs) > 0 {
		query = query.Where(dbaccount.ManagementFolderIDIn(folderIDs...))
	} else if filters.IncludeUncategorized {
		query = query.Where(dbaccount.ManagementFolderIDIsNil())
	}
	if tagIDs := uniquePositiveIDs(filters.TagIDs); len(tagIDs) > 0 {
		query = query.Where(dbaccount.HasTagsWith(dbaccounttag.IDIn(tagIDs...)))
	}
	proxyIDs := uniquePositiveIDs(filters.ProxyIDs)
	if len(proxyIDs) > 0 && filters.IncludeDirect {
		query = query.Where(dbaccount.Or(dbaccount.ProxyIDIn(proxyIDs...), dbaccount.ProxyIDIsNil()))
	} else if len(proxyIDs) > 0 {
		query = query.Where(dbaccount.ProxyIDIn(proxyIDs...))
	} else if filters.IncludeDirect {
		query = query.Where(dbaccount.ProxyIDIsNil())
	}

	field := map[string]string{
		"id": dbaccount.FieldID, "name": dbaccount.FieldName, "platform": dbaccount.FieldPlatform,
		"type": dbaccount.FieldType, "status": dbaccount.FieldStatus, "priority": dbaccount.FieldPriority,
		"concurrency": dbaccount.FieldConcurrency, "rate_multiplier": dbaccount.FieldRateMultiplier,
		"last_used_at": dbaccount.FieldLastUsedAt, "created_at": dbaccount.FieldCreatedAt,
		"updated_at": dbaccount.FieldUpdatedAt, "expires_at": dbaccount.FieldExpiresAt,
	}[filters.SortBy]
	if field == "" {
		field = dbaccount.FieldName
	}
	if strings.EqualFold(filters.SortOrder, "desc") {
		query = query.Order(dbent.Desc(field))
	} else {
		query = query.Order(dbent.Asc(field))
	}
	return query
}

func normalizeStringFilter(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func accountConsolePlan(account *Account) string {
	if account == nil {
		return ""
	}
	stringValue := func(source map[string]any, key string) string {
		if value, ok := source[key].(string); ok {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if account.Platform == PlatformGrok {
		for _, candidate := range []string{
			nestedStringValue(account.Extra, "grok_billing_snapshot", "plan"),
			nestedStringValue(account.Extra, "grok_quota_snapshot", "subscription_tier"),
			stringValue(account.Credentials, "subscription_tier"),
			stringValue(account.Extra, "subscription_tier"),
			stringValue(account.Credentials, "plan_type"),
		} {
			if candidate != "" {
				return candidate
			}
		}
	}
	return stringValue(account.Credentials, "plan_type")
}

func nestedStringValue(source map[string]any, objectKey, valueKey string) string {
	if object, ok := source[objectKey].(map[string]any); ok {
		if value, ok := object[valueKey].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func accountConsoleStatus(account *Account, now time.Time) string {
	if account == nil || account.Status != StatusActive {
		if account == nil {
			return ""
		}
		return account.Status
	}
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(now) {
		return "temp_unschedulable"
	}
	if account.RateLimitResetAt != nil && account.RateLimitResetAt.After(now) {
		return "rate_limited"
	}
	if account.hasCindyTerminalSchedulingBlock() || !account.Schedulable {
		return "unschedulable"
	}
	return StatusActive
}

func filterConsoleAccounts(accounts []*Account, filters AccountConsoleFilters) []*Account {
	statuses := make(map[string]struct{})
	for _, value := range normalizeStringFilter(filters.Statuses) {
		statuses[value] = struct{}{}
	}
	plans := make(map[string]struct{})
	for _, value := range normalizeStringFilter(filters.Plans) {
		plans[strings.ToLower(value)] = struct{}{}
	}
	if len(statuses) == 0 && len(plans) == 0 && !filters.CindyOnly && filters.CindyBalanceStatus == "" && filters.CindyHealthStatus == "" {
		return accounts
	}
	now := time.Now()
	out := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		isCindy := hasCanonicalCindyProviderIdentity(account)
		if filters.CindyOnly && !isCindy {
			continue
		}
		if filters.CindyBalanceStatus == "insufficient" && (!isCindy || account.CindyBalanceInsufficientAt == nil) {
			continue
		}
		if filters.CindyHealthStatus == "banned" && (!isCindy || account.CindyBannedAt == nil) {
			continue
		}
		if len(statuses) > 0 {
			if _, ok := statuses[accountConsoleStatus(account, now)]; !ok {
				continue
			}
		}
		if len(plans) > 0 {
			if _, ok := plans[strings.ToLower(accountConsolePlan(account))]; !ok {
				continue
			}
		}
		out = append(out, account)
	}
	return out
}

func (s *adminServiceImpl) listAccountConsoleAll(ctx context.Context, filters AccountConsoleFilters) ([]*Account, error) {
	ids, err := s.accountConsoleQuery(filters).IDs(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateAccountTaxonomy(ctx, accounts); err != nil {
		return nil, err
	}
	return filterConsoleAccounts(accounts, filters), nil
}

func (s *adminServiceImpl) ListAccountsConsole(ctx context.Context, page, pageSize int, filters AccountConsoleFilters) ([]Account, int64, error) {
	accounts, err := s.listAccountConsoleAll(ctx, filters)
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(accounts))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	if start >= len(accounts) {
		return []Account{}, total, nil
	}
	end := start + pageSize
	if end > len(accounts) {
		end = len(accounts)
	}
	out := make([]Account, 0, end-start)
	for _, account := range accounts[start:end] {
		out = append(out, *account)
	}
	if err := s.hydrateCindyBalanceProbeLatestValues(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func facetOptions(counts map[string]int) []AccountFacetOption {
	values := make([]string, 0, len(counts))
	for value := range counts {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return strings.ToLower(values[i]) < strings.ToLower(values[j]) })
	out := make([]AccountFacetOption, 0, len(values))
	for _, value := range values {
		out = append(out, AccountFacetOption{Value: value, Label: value, Count: counts[value]})
	}
	return out
}

type accountFacetDimension string

const (
	accountFacetNone          accountFacetDimension = ""
	accountFacetPlatforms     accountFacetDimension = "platforms"
	accountFacetTypes         accountFacetDimension = "types"
	accountFacetStatuses      accountFacetDimension = "statuses"
	accountFacetPlans         accountFacetDimension = "plans"
	accountFacetProxies       accountFacetDimension = "proxies"
	accountFacetFolders       accountFacetDimension = "folders"
	accountFacetTags          accountFacetDimension = "tags"
	accountFacetCindyIdentity accountFacetDimension = "cindy_identity"
	accountFacetCindyBalance  accountFacetDimension = "cindy_balance"
	accountFacetCindyHealth   accountFacetDimension = "cindy_health"
)

type accountFacetMatcher struct {
	platforms            map[string]struct{}
	types                map[string]struct{}
	statuses             map[string]struct{}
	plans                map[string]struct{}
	proxyIDs             map[int64]struct{}
	includeDirect        bool
	folderIDs            map[int64]struct{}
	includeUncategorized bool
	tagIDs               map[int64]struct{}
	cindyOnly            bool
	cindyBalanceStatus   string
	cindyHealthStatus    string
}

func stringFilterSet(values []string, lower bool) map[string]struct{} {
	out := make(map[string]struct{})
	for _, value := range normalizeStringFilter(values) {
		if lower {
			value = strings.ToLower(value)
		}
		out[value] = struct{}{}
	}
	return out
}

func int64FilterSet(values []int64) map[int64]struct{} {
	out := make(map[int64]struct{})
	for _, value := range uniquePositiveIDs(values) {
		out[value] = struct{}{}
	}
	return out
}

func newAccountFacetMatcher(filters AccountConsoleFilters) accountFacetMatcher {
	return accountFacetMatcher{
		platforms:            stringFilterSet(filters.Platforms, false),
		types:                stringFilterSet(filters.Types, false),
		statuses:             stringFilterSet(filters.Statuses, false),
		plans:                stringFilterSet(filters.Plans, true),
		proxyIDs:             int64FilterSet(filters.ProxyIDs),
		includeDirect:        filters.IncludeDirect,
		folderIDs:            int64FilterSet(filters.FolderIDs),
		includeUncategorized: filters.IncludeUncategorized,
		tagIDs:               int64FilterSet(filters.TagIDs),
		cindyOnly:            filters.CindyOnly,
		cindyBalanceStatus:   filters.CindyBalanceStatus,
		cindyHealthStatus:    filters.CindyHealthStatus,
	}
}

func (matcher accountFacetMatcher) matches(account *Account, ignored accountFacetDimension, now time.Time) bool {
	if account == nil {
		return false
	}
	if ignored != accountFacetPlatforms && len(matcher.platforms) > 0 {
		if _, ok := matcher.platforms[account.Platform]; !ok {
			return false
		}
	}
	if ignored != accountFacetTypes && len(matcher.types) > 0 {
		if _, ok := matcher.types[account.Type]; !ok {
			return false
		}
	}
	if ignored != accountFacetStatuses && len(matcher.statuses) > 0 {
		if _, ok := matcher.statuses[accountConsoleStatus(account, now)]; !ok {
			return false
		}
	}
	if ignored != accountFacetPlans && len(matcher.plans) > 0 {
		if _, ok := matcher.plans[strings.ToLower(accountConsolePlan(account))]; !ok {
			return false
		}
	}
	if ignored != accountFacetProxies && (len(matcher.proxyIDs) > 0 || matcher.includeDirect) {
		if account.ProxyID == nil {
			if !matcher.includeDirect {
				return false
			}
		} else if _, ok := matcher.proxyIDs[*account.ProxyID]; !ok {
			return false
		}
	}
	if ignored != accountFacetFolders && (len(matcher.folderIDs) > 0 || matcher.includeUncategorized) {
		folderID := account.ManagementFolderID
		if folderID == nil && account.ManagementFolder != nil {
			id := account.ManagementFolder.ID
			folderID = &id
		}
		if folderID == nil {
			if !matcher.includeUncategorized {
				return false
			}
		} else if _, ok := matcher.folderIDs[*folderID]; !ok {
			return false
		}
	}
	if ignored != accountFacetTags && len(matcher.tagIDs) > 0 {
		matched := false
		for _, tag := range account.Tags {
			if _, ok := matcher.tagIDs[tag.ID]; ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	isCindy := hasCanonicalCindyProviderIdentity(account)
	if ignored != accountFacetCindyIdentity && matcher.cindyOnly && !isCindy {
		return false
	}
	if ignored != accountFacetCindyBalance && matcher.cindyBalanceStatus == "insufficient" &&
		(!isCindy || account.CindyBalanceInsufficientAt == nil) {
		return false
	}
	if ignored != accountFacetCindyHealth && matcher.cindyHealthStatus == "banned" &&
		(!isCindy || account.CindyBannedAt == nil) {
		return false
	}
	return true
}

func filterAccountsForFacet(accounts []*Account, matcher accountFacetMatcher, ignored accountFacetDimension, now time.Time) []*Account {
	out := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if matcher.matches(account, ignored, now) {
			out = append(out, account)
		}
	}
	return out
}

func (s *adminServiceImpl) GetAccountConsoleFacets(ctx context.Context, filters AccountConsoleFilters) (*AccountConsoleFacets, error) {
	baseFilters := filters
	baseFilters.Platforms = nil
	baseFilters.Types = nil
	baseFilters.Statuses = nil
	baseFilters.Plans = nil
	baseFilters.ProxyIDs = nil
	baseFilters.IncludeDirect = false
	baseFilters.FolderIDs = nil
	baseFilters.IncludeUncategorized = false
	baseFilters.TagIDs = nil
	baseFilters.CindyOnly = false
	baseFilters.CindyBalanceStatus = ""
	baseFilters.CindyHealthStatus = ""
	accounts, err := s.listAccountConsoleAll(ctx, baseFilters)
	if err != nil {
		return nil, err
	}
	matcher := newAccountFacetMatcher(filters)
	now := time.Now()
	platformAccounts := filterAccountsForFacet(accounts, matcher, accountFacetPlatforms, now)
	typeAccounts := filterAccountsForFacet(accounts, matcher, accountFacetTypes, now)
	statusAccounts := filterAccountsForFacet(accounts, matcher, accountFacetStatuses, now)
	planAccounts := filterAccountsForFacet(accounts, matcher, accountFacetPlans, now)
	proxyAccounts := filterAccountsForFacet(accounts, matcher, accountFacetProxies, now)
	folderAccounts := filterAccountsForFacet(accounts, matcher, accountFacetFolders, now)
	tagAccounts := filterAccountsForFacet(accounts, matcher, accountFacetTags, now)
	cindyAccounts := filterAccountsForFacet(accounts, matcher, accountFacetCindyIdentity, now)
	cindyBalanceAccounts := filterAccountsForFacet(accounts, matcher, accountFacetCindyBalance, now)
	cindyHealthAccounts := filterAccountsForFacet(accounts, matcher, accountFacetCindyHealth, now)
	platforms, types, statuses, plans, proxies := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	folderCounts, tagCounts := map[int64]int{}, map[int64]int{}
	uncategorizedCount := 0
	cindyTotal, cindyInsufficient, cindyBanned := 0, 0, 0
	for _, account := range platformAccounts {
		platforms[account.Platform]++
	}
	for _, account := range typeAccounts {
		types[account.Type]++
	}
	for _, account := range statusAccounts {
		statuses[accountConsoleStatus(account, now)]++
	}
	for _, account := range planAccounts {
		if plan := accountConsolePlan(account); plan != "" {
			plans[plan]++
		}
	}
	for _, account := range proxyAccounts {
		if account.Proxy == nil {
			proxies["direct"]++
		} else {
			proxies[strconv.FormatInt(account.Proxy.ID, 10)+"|"+account.Proxy.Name]++
		}
	}
	for _, account := range folderAccounts {
		if account.ManagementFolder != nil {
			folderCounts[account.ManagementFolder.ID]++
		} else {
			uncategorizedCount++
		}
	}
	for _, account := range tagAccounts {
		for _, tag := range account.Tags {
			tagCounts[tag.ID]++
		}
	}
	for _, account := range cindyAccounts {
		if !hasCanonicalCindyProviderIdentity(account) {
			continue
		}
		cindyTotal++
	}
	for _, account := range cindyBalanceAccounts {
		if !hasCanonicalCindyProviderIdentity(account) {
			continue
		}
		if account.CindyBalanceInsufficientAt != nil {
			cindyInsufficient++
		}
	}
	for _, account := range cindyHealthAccounts {
		if !hasCanonicalCindyProviderIdentity(account) {
			continue
		}
		if account.CindyBannedAt != nil {
			cindyBanned++
		}
	}
	folders, err := s.listAccountFolders(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range folders {
		folders[i].AccountCount = folderCounts[folders[i].ID]
	}
	tags, err := s.listAccountTags(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range tags {
		tags[i].AccountCount = tagCounts[tags[i].ID]
	}
	proxyOptions := make([]AccountFacetOption, 0, len(proxies))
	for encoded, count := range proxies {
		value, label := encoded, encoded
		if encoded == "direct" {
			label = "Direct"
		} else if separator := strings.IndexByte(encoded, '|'); separator > 0 {
			value, label = encoded[:separator], encoded[separator+1:]
		}
		proxyOptions = append(proxyOptions, AccountFacetOption{Value: value, Label: label, Count: count})
	}
	sort.Slice(proxyOptions, func(i, j int) bool {
		return strings.ToLower(proxyOptions[i].Label) < strings.ToLower(proxyOptions[j].Label)
	})
	return &AccountConsoleFacets{
		// Folder navigation always represents the complete result set after all
		// non-folder filters, so total must use the same population as its counts.
		Total: len(folderAccounts), UncategorizedCount: uncategorizedCount,
		Platforms: facetOptions(platforms), Types: facetOptions(types),
		Statuses: facetOptions(statuses), Plans: facetOptions(plans), Proxies: proxyOptions,
		Folders: folders, Tags: tags,
		CindyTotal: cindyTotal, CindyInsufficient: cindyInsufficient, CindyBanned: cindyBanned,
	}, nil
}
