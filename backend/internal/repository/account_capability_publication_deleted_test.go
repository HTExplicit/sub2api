//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const publicationDeletedAccountSQL = `SELECT id,management_folder_id,platform,provider_profile,parent_account_id,deleted_at FROM accounts WHERE id=ANY($1) ORDER BY id FOR UPDATE`
const publicationDeletedLedgerSQL = `SELECT i.account_id,i.id,i.folder_id,i.config_fingerprint,r.folder_ids,r.account_ids
		FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id
		WHERE i.account_id=ANY($1) AND NOT EXISTS(SELECT 1 FROM admin_capability_items newer WHERE newer.account_id=i.account_id AND newer.id>i.id)
		ORDER BY i.account_id FOR SHARE OF i,r`

func publicationDeletedRepoGroup() *service.Group {
	return &service.Group{
		ID: 23, Name: "fixture-public-group", Platform: service.PlatformOpenAI, WirePlatform: service.PlatformOpenAI,
		RateMultiplier: 0.2, Status: service.StatusActive,
		ManagedModelRoutes: domain.ManagedModelRoutesConfig{
			Version: 1, Enabled: true,
			Routes: []domain.ManagedModelRoute{{
				PublicModel: "gpt-5.6-sol", Selector: service.ManagedModelSelector(23, "gpt-5.6-sol"), TargetPlatform: service.PlatformOpenAI,
				Endpoints: []string{"responses"},
				Accounts:  []domain.ManagedModelRouteAccount{{AccountID: 8, UpstreamModel: "gpt-5.6-sol", AccountFingerprint: "fixture-deleted-fingerprint", Endpoints: []string{"responses"}}},
			}},
		},
	}
}

func publicationDeletedRepoFixture() *service.CapabilityPublicationSnapshot {
	return &service.CapabilityPublicationSnapshot{
		Accounts: map[int64]*service.CapabilityPublicationAccountSnapshot{31: {Account: &service.Account{ID: 31}, Bindings: map[int64]int{}}},
		Groups: map[int64]*service.CapabilityPublicationGroupSnapshot{
			23: {Group: publicationDeletedRepoGroup(), Bindings: map[int64]int{8: 19}},
		},
	}
}

func publicationDeletedRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "management_folder_id", "platform", "provider_profile", "parent_account_id", "deleted_at"})
}

func TestCapabilityPublicationDeletedSnapshotLoadsStoredReferencesAndBindings(t *testing.T) {
	for _, source := range []string{"soft_deleted", "latest_ledger", "unknown_ledger"} {
		t.Run(source, func(t *testing.T) {
			repo, mock := newPublicationRepoTest(t)
			snap := publicationDeletedRepoFixture()
			mock.ExpectBegin()
			rows := publicationDeletedRows()
			deletedAt := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
			if source == "soft_deleted" {
				rows.AddRow(8, 9, "openai", "", nil, deletedAt)
			}
			mock.ExpectQuery(regexp.QuoteMeta(publicationDeletedAccountSQL)).WithArgs("{8}").WillReturnRows(rows)
			if source != "soft_deleted" {
				ledger := sqlmock.NewRows([]string{"account_id", "id", "folder_id", "config_fingerprint", "folder_ids", "account_ids"})
				if source == "latest_ledger" {
					ledger.AddRow(8, 91, 9, "fixture-deleted-fingerprint", []byte(`[9]`), []byte(`[8]`))
				}
				mock.ExpectQuery(regexp.QuoteMeta(publicationDeletedLedgerSQL)).WithArgs("{8}").WillReturnRows(ledger)
			}
			mock.ExpectRollback()
			tx, err := repo.db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			require.NoError(t, publicationLoadDeletedAccounts(context.Background(), tx, snap, []int64{8, 31}))
			require.Len(t, snap.Accounts, 1, "deleted records must not become selectable live accounts")
			require.NotContains(t, snap.Accounts, int64(8))
			d := snap.DeletedAccounts[8]
			require.NotNil(t, d)
			require.Equal(t, source != "soft_deleted", d.Missing)
			switch source {
			case "soft_deleted":
				require.Equal(t, &deletedAt, d.DeletedAt)
				require.Equal(t, int64(9), *d.FolderID)
				require.Nil(t, d.Evidence)
			case "latest_ledger":
				require.Nil(t, d.DeletedAt)
				require.Equal(t, int64(9), *d.FolderID)
				require.Equal(t, int64(91), d.Evidence.ID)
				require.Equal(t, []int64{8}, d.Evidence.RunAccountIDs)
			case "unknown_ledger":
				// Unknown provenance may be observed for a merge that does not
				// remove this account; the service rejects selecting it for cleanup.
				require.Nil(t, d.FolderID)
				require.Nil(t, d.Evidence)
			}
			require.NoError(t, publicationRecordAccountBinding(snap, 8, 23, 19))
			require.NoError(t, publicationRecordAccountBinding(snap, 8, 55, 17))
			require.Equal(t, map[int64]int{23: 19, 55: 17}, d.Bindings)
			require.NoError(t, publicationRecordAccountBinding(snap, 31, 93, 7))
			require.Equal(t, 7, snap.Accounts[31].Bindings[93])
			require.ErrorIs(t, publicationRecordAccountBinding(snap, 99, 23, 1), service.ErrCapabilityPublicationConflict)
			require.NoError(t, tx.Rollback())
		})
	}
}

func TestCapabilityPublicationDeletedSnapshotRejectsUnreferencedOrRestoredAccounts(t *testing.T) {
	for _, reason := range []string{"unreferenced", "restored"} {
		t.Run(reason, func(t *testing.T) {
			repo, mock := newPublicationRepoTest(t)
			snap := publicationDeletedRepoFixture()
			mock.ExpectBegin()
			id := int64(99)
			if reason == "restored" {
				id = 8
				mock.ExpectQuery(regexp.QuoteMeta(publicationDeletedAccountSQL)).WithArgs("{8}").
					WillReturnRows(publicationDeletedRows().AddRow(8, 9, "openai", "", nil, nil))
			}
			mock.ExpectRollback()
			tx, err := repo.db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			require.ErrorIs(t, publicationLoadDeletedAccounts(context.Background(), tx, snap, []int64{id, 31}), service.ErrCapabilityPublicationConflict)
			require.NoError(t, tx.Rollback())
		})
	}
}

func publicationDeletedFullSnapshot(t *testing.T, mock sqlmock.Sqlmock, changed string) {
	t.Helper()
	group := publicationDeletedRepoGroup()
	if changed == "browser_route" {
		group.ManagedModelRoutes.Routes[0].Aliases = []string{"browser-added-alias"}
	}
	rawGroup, err := json.Marshal(publicationGroupRow{
		ID: group.ID, Name: group.Name, Platform: group.Platform, WirePlatform: group.WirePlatform,
		RateMultiplier: group.RateMultiplier, Status: group.Status, ManagedModelRoutes: group.ManagedModelRoutes,
	})
	require.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT to_jsonb(g)-'updated_at' FROM groups g WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`)).
		WithArgs("{23}").WillReturnRows(sqlmock.NewRows([]string{"group"}).AddRow(rawGroup))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id,group_id,priority FROM account_groups WHERE group_id=ANY($1) ORDER BY group_id,account_id FOR UPDATE`)).
		WithArgs("{23}").WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority"}).AddRow(8, 23, 19))
	accounts := sqlmock.NewRows([]string{"id", "name", "platform", "wire_platform", "provider_profile", "type", "credentials", "extra", "proxy_id", "management_folder_id", "parent_account_id", "schedulable", "status"})
	if changed == "restored_account" {
		accounts.AddRow(8, "restored-account", "openai", "openai", "", "apikey", []byte(`{"model_mapping":{"private-model":"private-upstream"}}`), []byte(`{}`), nil, 9, nil, true, "active")
	}
	accounts.AddRow(31, "live-account", "openai", "openai", "", "apikey", []byte(`{"model_mapping":{"private-model":"private-upstream"}}`), []byte(`{}`), nil, 9, nil, true, "active")
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,name,platform,wire_platform,provider_profile,type,credentials,extra,proxy_id,management_folder_id,parent_account_id,schedulable,status FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`)).
		WithArgs("{8,31}").WillReturnRows(accounts)
	if changed != "restored_account" {
		rows := publicationDeletedRows()
		if changed != "physically_missing" {
			folderID := int64(9)
			if changed == "deleted_folder" {
				folderID = 99
			}
			rows.AddRow(8, folderID, "openai", "", nil, time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC))
		}
		mock.ExpectQuery(regexp.QuoteMeta(publicationDeletedAccountSQL)).WithArgs("{8}").WillReturnRows(rows)
		if changed == "physically_missing" {
			mock.ExpectQuery(regexp.QuoteMeta(publicationDeletedLedgerSQL)).WithArgs("{8}").
				WillReturnRows(sqlmock.NewRows([]string{"account_id", "id", "folder_id", "config_fingerprint", "folder_ids", "account_ids"}).
					AddRow(8, 91, 9, "fixture-deleted-fingerprint", []byte(`[9]`), []byte(`[8]`)))
		}
	}
	privatePriority := 17
	if changed == "deleted_private_binding" {
		privatePriority = 27
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id,group_id,priority FROM account_groups WHERE account_id=ANY($1) ORDER BY account_id,group_id FOR UPDATE`)).
		WithArgs("{8,31}").WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority"}).AddRow(8, 23, 19).AddRow(8, 55, privatePriority).AddRow(31, 93, 7))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT channel_id FROM channel_groups WHERE group_id=$1 FOR UPDATE`)).
		WithArgs(int64(23)).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,group_id,public_model,match_type,target_platform,upstream_model,endpoint,priority,enabled,COALESCE(notes,''),created_at,updated_at FROM composite_model_routes WHERE group_id=$1 AND deleted_at IS NULL ORDER BY priority,id FOR UPDATE`)).
		WithArgs(int64(23)).WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "public_model", "match_type", "target_platform", "upstream_model", "endpoint", "priority", "enabled", "notes", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT i\.id,i\.account_id,i\.folder_id,[\s\S]+` + regexp.QuoteMeta(capabilityPublicationSupersededSQL) + `[\s\S]+FOR SHARE OF i,r`).
		WithArgs("{}").WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "folder_id", "config_fingerprint", "upstream_model", "protocol", "profile", "status", "result", "finished_at", "kind", "folder_ids", "account_ids", "superseded"}))
}

func TestCapabilityPublicationDeletedApplyRejectsCASDriftBeforeMutation(t *testing.T) {
	for _, changed := range []string{"restored_account", "deleted_folder", "physically_missing", "deleted_private_binding", "browser_route"} {
		t.Run(changed, func(t *testing.T) {
			repo, mock := newPublicationRepoTest(t)
			request := service.CapabilityPublicationRequest{
				IdempotencyKey: "fixture-deleted-cleanup",
				Scope:          service.CapabilityPublicationScope{FolderIDs: []int64{9}, AccountIDs: []int64{8, 31}},
				Groups:         []service.CapabilityPublicationGroup{{ID: 23, Name: "fixture-public-group", Platform: service.PlatformOpenAI, RateMultiplier: 0.2, RemoveModels: []string{"gpt-5.6-sol"}}},
			}
			publicationTestBegin(mock)
			mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtextextended($1, 481627))`)).WithArgs(request.IdempotencyKey).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,status,request,plan,before_fingerprint,created_at,applied_at FROM admin_capability_changesets WHERE idempotency_key = $1`)).
				WithArgs(request.IdempotencyKey).WillReturnError(sql.ErrNoRows)
			publicationDeletedFullSnapshot(t, mock, "")
			mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO admin_capability_changesets(idempotency_key,request,plan,before_fingerprint,status) VALUES($1,$2::jsonb,$3::jsonb,$4,'preview') RETURNING id,created_at`)).
				WithArgs(request.IdempotencyKey, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(71, time.Unix(101, 0).UTC()))
			mock.ExpectCommit()
			set, err := repo.Preview(context.Background(), request, func(snap *service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
				require.NotNil(t, snap.DeletedAccounts[8])
				require.Equal(t, 17, snap.DeletedAccounts[8].Bindings[55])
				return &service.CapabilityPublicationPlan{}, nil
			})
			require.NoError(t, err)
			publicationTestBegin(mock)
			publicationTestExpectRead(mock, t, set, true)
			publicationDeletedFullSnapshot(t, mock, changed)
			mock.ExpectRollback()
			got, err := repo.Apply(context.Background(), set.ID, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
				t.Fatal("deleted-state/browser CAS drift reached the mutation-plan builder")
				return nil, nil
			})
			require.ErrorIs(t, err, service.ErrCapabilityPublicationConflict)
			require.Nil(t, got)
		})
	}
}

func TestCapabilityPublicationDeletedApplyAccountOnlyRemovesNamedPublicBinding(t *testing.T) {
	repo, mock := newPublicationRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`)).
		WithArgs(int64(8), int64(23)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`)).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(8), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload) VALUES ($1, $2, $3, $4)`)).
		WithArgs(service.SchedulerOutboxEventAccountGroupsChanged, int64(8), nil, []byte(`{"group_ids":[23]}`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	tx, err := repo.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, publicationApplyAccount(context.Background(), tx, service.CapabilityPublicationAccountPatch{AccountID: 8, RemoveGroupIDs: []int64{23}}))
	require.NoError(t, tx.Commit())
}
