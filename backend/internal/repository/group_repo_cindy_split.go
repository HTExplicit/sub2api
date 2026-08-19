package repository

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type cindyGroupSplitMember struct {
	accountID int64
	priority  int
	isCindy   bool
}

type cindyGroupSplitAPIKey struct {
	id int64
}

type cindyGroupSplitSnapshotData struct {
	public  service.CindyGroupSplitRepositorySnapshot
	members []cindyGroupSplitMember
	apiKeys []cindyGroupSplitAPIKey
}

// AuditCindyGroups classifies every OpenAI group using complete non-deleted
// membership. Account identities never leave the repository.
func (r *groupRepository) AuditCindyGroups(ctx context.Context) ([]service.CindyGroupAuditEntry, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("group repository SQL executor is unavailable")
	}
	rows, err := r.sql.QueryContext(ctx, `
		WITH account_counts AS (
			SELECT
				ag.group_id,
				COUNT(*) AS account_count,
				COALESCE(SUM(CASE WHEN
					a.platform = $2
					AND a.type = $3
					AND LOWER(TRIM(a.credentials ->> 'base_url')) IN ($4, $5)
				THEN 1 ELSE 0 END), 0) AS cindy_account_count
			FROM account_groups ag
			JOIN accounts a ON a.id = ag.account_id
			WHERE a.deleted_at IS NULL
			GROUP BY ag.group_id
		), key_counts AS (
			SELECT group_id, COUNT(*) AS api_key_count
			FROM api_keys
			WHERE deleted_at IS NULL AND group_id IS NOT NULL
			GROUP BY group_id
		)
		SELECT
			g.id,
			g.name,
			g.status,
			COALESCE(ac.account_count, 0),
			COALESCE(ac.cindy_account_count, 0),
			COALESCE(kc.api_key_count, 0)
		FROM groups g
		LEFT JOIN account_counts ac ON ac.group_id = g.id
		LEFT JOIN key_counts kc ON kc.group_id = g.id
		WHERE g.platform = $1
			AND g.deleted_at IS NULL
		ORDER BY g.sort_order ASC, g.id ASC
	`,
		service.PlatformOpenAI,
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		"https://api.laxarouter.ai",
		"https://api.laxarouter.ai/",
	)
	if err != nil {
		return nil, fmt.Errorf("query Cindy group audit: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]service.CindyGroupAuditEntry, 0)
	for rows.Next() {
		var entry service.CindyGroupAuditEntry
		var accountCount int64
		if err := rows.Scan(
			&entry.GroupID,
			&entry.GroupName,
			&entry.Status,
			&accountCount,
			&entry.CindyAccountCount,
			&entry.APIKeyCount,
		); err != nil {
			return nil, fmt.Errorf("scan Cindy group audit: %w", err)
		}
		entry.OrdinaryAccountCount = accountCount - entry.CindyAccountCount
		entry.Classification = classifyCindyGroupCounts(entry.CindyAccountCount, entry.OrdinaryAccountCount)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Cindy group audit: %w", err)
	}
	return entries, nil
}

// PreviewCindyGroupSplit returns a read-only source snapshot and CAS fingerprint.
func (r *groupRepository) PreviewCindyGroupSplit(ctx context.Context, groupID int64, input service.CindyGroupSplitInput) (*service.CindyGroupSplitRepositorySnapshot, error) {
	if r == nil || r.client == nil || r.sql == nil {
		return nil, errors.New("group repository is unavailable")
	}
	snapshot, err := loadCindyGroupSplitSnapshot(ctx, r.client, r.sql, groupID, input, false)
	if err != nil {
		return nil, err
	}
	return &snapshot.public, nil
}

// CommitCindyGroupSplit locks and revalidates the source snapshot before making
// all group, account-membership, API-key, and scheduler-outbox writes atomically.
func (r *groupRepository) CommitCindyGroupSplit(ctx context.Context, groupID int64, input service.CindyGroupSplitInput) (*service.CindyGroupSplitResult, error) {
	if r == nil || r.client == nil {
		return nil, errors.New("group repository is unavailable")
	}

	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return nil, fmt.Errorf("begin Cindy group split transaction: %w", err)
	}
	txClient := r.client
	if tx != nil {
		defer func() { _ = tx.Rollback() }()
		txClient = tx.Client()
	}

	if err := lockCindySplitGroup(ctx, txClient, groupID); err != nil {
		return nil, err
	}
	snapshot, err := loadCindyGroupSplitSnapshot(ctx, txClient, txClient, groupID, input, true)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(snapshot.public.Preview.MemberFingerprint), []byte(input.MemberFingerprint)) != 1 {
		return nil, service.ErrCindyGroupSplitDrift
	}

	target := service.BuildCindySplitTargetGroup(snapshot.public.SourceGroup, input.TargetName)
	target.ID = 0
	target.Name = input.TargetName
	target.Platform = service.PlatformOpenAI
	target.DuplicateOperationID = ""
	target.CreatedAt = time.Time{}
	target.UpdatedAt = time.Time{}
	if err := createGroupRecord(ctx, txClient, target); err != nil {
		return nil, fmt.Errorf("create Cindy split target group: %w", err)
	}

	movedIDs := cindyGroupAccountsToMove(snapshot.members, input.SourceKeeps)
	insertResult, err := txClient.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority, created_at)
		SELECT account_id, $2, priority, NOW()
		FROM account_groups
		WHERE group_id = $1 AND account_id = ANY($3)
		ON CONFLICT (account_id, group_id) DO NOTHING
	`, groupID, target.ID, movedIDs)
	if err != nil {
		return nil, fmt.Errorf("insert Cindy split target memberships: %w", err)
	}
	inserted, err := insertResult.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("count Cindy split target memberships: %w", err)
	}
	if inserted != int64(len(movedIDs)) {
		return nil, service.ErrCindyGroupSplitDrift
	}

	deleteResult, err := txClient.ExecContext(ctx,
		"DELETE FROM account_groups WHERE group_id = $1 AND account_id = ANY($2)",
		groupID,
		movedIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("delete Cindy split source memberships: %w", err)
	}
	deleted, err := deleteResult.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("count Cindy split source memberships: %w", err)
	}
	if deleted != int64(len(movedIDs)) {
		return nil, service.ErrCindyGroupSplitDrift
	}

	if len(input.APIKeyIDs) > 0 {
		keyResult, err := txClient.ExecContext(ctx, `
			UPDATE api_keys
			SET group_id = $2, updated_at = NOW()
			WHERE group_id = $1 AND id = ANY($3) AND deleted_at IS NULL
		`, groupID, target.ID, input.APIKeyIDs)
		if err != nil {
			return nil, fmt.Errorf("rebind Cindy split API keys: %w", err)
		}
		rebound, err := keyResult.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count rebound Cindy split API keys: %w", err)
		}
		if rebound != int64(len(input.APIKeyIDs)) {
			return nil, service.ErrCindyGroupSplitDrift
		}
	}

	bulkPayload := map[string]any{
		"account_ids": movedIDs,
		"group_ids":   []int64{groupID, target.ID},
	}
	if err := enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventAccountBulkChanged, nil, nil, bulkPayload); err != nil {
		return nil, fmt.Errorf("enqueue Cindy split account event: %w", err)
	}
	if err := enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil); err != nil {
		return nil, fmt.Errorf("enqueue Cindy split source group event: %w", err)
	}
	if err := enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventGroupChanged, nil, &target.ID, nil); err != nil {
		return nil, fmt.Errorf("enqueue Cindy split target group event: %w", err)
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit Cindy group split transaction: %w", err)
		}
	}
	return &service.CindyGroupSplitResult{
		CindyGroupSplitPreview: snapshot.public.Preview,
		TargetGroupID:          target.ID,
	}, nil
}

func loadCindyGroupSplitSnapshot(
	ctx context.Context,
	client *dbent.Client,
	exec sqlExecutor,
	groupID int64,
	input service.CindyGroupSplitInput,
	lockRows bool,
) (*cindyGroupSplitSnapshotData, error) {
	entity, err := client.Group.Query().Where(dbgroup.IDEQ(groupID)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, service.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load Cindy split source group: %w", err)
	}
	source := groupEntityToService(entity)
	if source.Platform != service.PlatformOpenAI {
		return nil, service.ErrCindyGroupNotOpenAI
	}
	if strings.EqualFold(strings.TrimSpace(source.Name), strings.TrimSpace(input.TargetName)) {
		return nil, service.ErrCindyGroupInvalidInput
	}
	exists, err := client.Group.Query().Where(dbgroup.NameEQ(input.TargetName)).Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("check Cindy split target group name: %w", err)
	}
	if exists {
		return nil, service.ErrGroupExists
	}

	members, err := loadCindyGroupSplitMembers(ctx, exec, groupID, lockRows)
	if err != nil {
		return nil, err
	}
	apiKeys, err := loadCindyGroupSplitAPIKeys(ctx, exec, groupID, lockRows)
	if err != nil {
		return nil, err
	}
	if err := validateCindySplitAPIKeySelection(apiKeys, input.APIKeyIDs); err != nil {
		return nil, err
	}

	var cindyCount int64
	for _, member := range members {
		if member.isCindy {
			cindyCount++
		}
	}
	ordinaryCount := int64(len(members)) - cindyCount
	if classifyCindyGroupCounts(cindyCount, ordinaryCount) != service.CindyGroupClassificationMixed {
		return nil, service.ErrCindyGroupNotMixed
	}
	targetClassification := service.CindyGroupClassificationNoCindy
	accountsToMove := ordinaryCount
	if input.SourceKeeps == service.CindyGroupSourceKeepsOrdinary {
		targetClassification = service.CindyGroupClassificationPureCindy
		accountsToMove = cindyCount
	}

	preview := service.CindyGroupSplitPreview{
		SourceGroupID:        source.ID,
		SourceGroupName:      source.Name,
		SourceKeeps:          input.SourceKeeps,
		TargetName:           input.TargetName,
		TargetClassification: targetClassification,
		CindyAccountCount:    cindyCount,
		OrdinaryAccountCount: ordinaryCount,
		AccountsToMove:       accountsToMove,
		SourceAPIKeyCount:    int64(len(apiKeys)),
		APIKeysToRebind:      int64(len(input.APIKeyIDs)),
		APIKeysRemaining:     int64(len(apiKeys) - len(input.APIKeyIDs)),
	}
	preview.MemberFingerprint, err = cindyGroupSplitFingerprint(source, members, apiKeys, input)
	if err != nil {
		return nil, err
	}
	return &cindyGroupSplitSnapshotData{
		public: service.CindyGroupSplitRepositorySnapshot{
			SourceGroup: source,
			Preview:     preview,
		},
		members: members,
		apiKeys: apiKeys,
	}, nil
}

func lockCindySplitGroup(ctx context.Context, exec sqlExecutor, groupID int64) error {
	rows, err := exec.QueryContext(ctx, "SELECT id FROM groups WHERE id = $1 FOR UPDATE", groupID)
	if err != nil {
		return fmt.Errorf("lock Cindy split source group: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("lock Cindy split source group: %w", err)
		}
		return service.ErrGroupNotFound
	}
	var lockedID int64
	if err := rows.Scan(&lockedID); err != nil {
		return fmt.Errorf("scan locked Cindy split source group: %w", err)
	}
	return nil
}

func loadCindyGroupSplitMembers(ctx context.Context, exec sqlExecutor, groupID int64, lockRows bool) ([]cindyGroupSplitMember, error) {
	query := `
		SELECT
			a.id,
			ag.priority,
			CASE WHEN
				a.platform = $2
				AND a.type = $3
				AND LOWER(TRIM(a.credentials ->> 'base_url')) IN ($4, $5)
			THEN TRUE ELSE FALSE END
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = $1 AND a.deleted_at IS NULL
		ORDER BY a.id ASC
	`
	if lockRows {
		query += " FOR UPDATE OF ag, a"
	}
	rows, err := exec.QueryContext(ctx, query,
		groupID,
		service.PlatformOpenAI,
		service.AccountTypeAPIKey,
		"https://api.laxarouter.ai",
		"https://api.laxarouter.ai/",
	)
	if err != nil {
		return nil, fmt.Errorf("query Cindy split members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	members := make([]cindyGroupSplitMember, 0)
	for rows.Next() {
		var member cindyGroupSplitMember
		if err := rows.Scan(
			&member.accountID,
			&member.priority,
			&member.isCindy,
		); err != nil {
			return nil, fmt.Errorf("scan Cindy split member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Cindy split members: %w", err)
	}
	return members, nil
}

func loadCindyGroupSplitAPIKeys(ctx context.Context, exec sqlExecutor, groupID int64, lockRows bool) ([]cindyGroupSplitAPIKey, error) {
	query := `
		SELECT k.id
		FROM api_keys k
		WHERE k.group_id = $1 AND k.deleted_at IS NULL
		ORDER BY k.id ASC
	`
	if lockRows {
		query += " FOR UPDATE OF k"
	}
	rows, err := exec.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("query Cindy split API keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([]cindyGroupSplitAPIKey, 0)
	for rows.Next() {
		var key cindyGroupSplitAPIKey
		if err := rows.Scan(&key.id); err != nil {
			return nil, fmt.Errorf("scan Cindy split API key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Cindy split API keys: %w", err)
	}
	return keys, nil
}

func validateCindySplitAPIKeySelection(keys []cindyGroupSplitAPIKey, selected []int64) error {
	available := make(map[int64]struct{}, len(keys))
	for _, key := range keys {
		available[key.id] = struct{}{}
	}
	for _, id := range selected {
		if _, ok := available[id]; !ok {
			return service.ErrCindyGroupAPIKeySelection
		}
	}
	return nil
}

func classifyCindyGroupCounts(cindyCount, ordinaryCount int64) string {
	switch {
	case cindyCount > 0 && ordinaryCount == 0:
		return service.CindyGroupClassificationPureCindy
	case cindyCount > 0 && ordinaryCount > 0:
		return service.CindyGroupClassificationMixed
	default:
		return service.CindyGroupClassificationNoCindy
	}
}

func cindyGroupAccountsToMove(members []cindyGroupSplitMember, sourceKeeps string) []int64 {
	moveCindy := sourceKeeps == service.CindyGroupSourceKeepsOrdinary
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		if member.isCindy == moveCindy {
			ids = append(ids, member.accountID)
		}
	}
	return ids
}

// cindyGroupSplitFingerprint covers only state that can change the split's
// writes. Request-time account/key status and timestamp churn is deliberately
// excluded; strict identity changes are represented by the derived isCindy bit.
func cindyGroupSplitFingerprint(source *service.Group, members []cindyGroupSplitMember, keys []cindyGroupSplitAPIKey, input service.CindyGroupSplitInput) (string, error) {
	targetPolicy, err := json.Marshal(service.BuildCindySplitTargetGroup(source, input.TargetName))
	if err != nil {
		return "", fmt.Errorf("marshal Cindy split target policy: %w", err)
	}
	fields := make([]string, 0, 12+len(members)*3+len(keys)+len(input.APIKeyIDs))
	appendField := func(value string) {
		fields = append(fields, strconv.Itoa(len(value)), value)
	}
	appendField("cindy-group-split-v2")
	appendField(strconv.FormatInt(source.ID, 10))
	appendField(string(targetPolicy))
	appendField(input.SourceKeeps)
	appendField(input.TargetName)

	for _, member := range members {
		appendField(strconv.FormatInt(member.accountID, 10))
		appendField(strconv.Itoa(member.priority))
		appendField(strconv.FormatBool(member.isCindy))
	}
	for _, key := range keys {
		appendField(strconv.FormatInt(key.id, 10))
	}
	selected := append([]int64(nil), input.APIKeyIDs...)
	sort.Slice(selected, func(i, j int) bool { return selected[i] < selected[j] })
	for _, id := range selected {
		appendField(strconv.FormatInt(id, 10))
	}
	digest := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return fmt.Sprintf("%x", digest), nil
}
