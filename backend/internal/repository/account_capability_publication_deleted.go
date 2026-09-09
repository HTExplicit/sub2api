package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Only identifiers already present in a locked group reference can become
// deletion tombstones. A missing arbitrary scope/evidence account remains a
// conflict, rather than acquiring deletion authority merely from its absence.
func publicationHasStoredAccountReference(snap *service.CapabilityPublicationSnapshot, id int64) bool {
	for _, gs := range snap.Groups {
		if _, bound := gs.Bindings[id]; bound {
			return true
		}
		for _, route := range gs.Group.ManagedModelRoutes.Routes {
			for _, branch := range service.ManagedModelRouteBranches(route) {
				for _, member := range branch.Accounts {
					if member.AccountID == id {
						return true
					}
				}
			}
		}
	}
	return false
}

func publicationLoadDeletedAccounts(ctx context.Context, tx *sql.Tx, snap *service.CapabilityPublicationSnapshot, ids []int64) error {
	missing := map[int64]bool{}
	for _, id := range ids {
		if snap.Accounts[id] != nil {
			continue
		}
		if !publicationHasStoredAccountReference(snap, id) {
			return service.ErrCapabilityPublicationConflict
		}
		missing[id] = true
	}
	if len(missing) == 0 {
		return nil
	}
	snap.DeletedAccounts = make(map[int64]*service.CapabilityPublicationDeletedAccountSnapshot, len(missing))
	for id := range missing {
		snap.DeletedAccounts[id] = &service.CapabilityPublicationDeletedAccountSnapshot{Missing: true, Bindings: map[int64]int{}}
	}
	// Do not filter deleted_at here: a restored/live row is a conflict, not a
	// tombstone. The serializable transaction also protects physically absent
	// identifiers against concurrent resurrection or legacy binding writers.
	rows, err := tx.QueryContext(ctx, `SELECT id,management_folder_id,platform,provider_profile,parent_account_id,deleted_at FROM accounts WHERE id=ANY($1) ORDER BY id FOR UPDATE`, pq.Array(publicationSortedIDs(missing)))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		d := &service.CapabilityPublicationDeletedAccountSnapshot{Bindings: map[int64]int{}}
		if err = rows.Scan(&id, &d.FolderID, &d.Platform, &d.ProviderProfile, &d.ParentAccountID, &d.DeletedAt); err != nil {
			_ = rows.Close()
			return err
		}
		if !missing[id] || d.DeletedAt == nil {
			_ = rows.Close()
			return service.ErrCapabilityPublicationConflict
		}
		snap.DeletedAccounts[id] = d
		delete(missing, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || len(missing) == 0 {
		return err
	}
	// Account rows may be gone, but the durable diagnostic ledger deliberately
	// has no cascading account FK. Read the latest item across all run kinds and
	// folders; never choose an older favorable in-scope record. The observation
	// is not liveness evidence and remains outside the publication evidence set.
	rows, err = tx.QueryContext(ctx, `SELECT i.account_id,i.id,i.folder_id,i.config_fingerprint,r.folder_ids,r.account_ids
		FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id
		WHERE i.account_id=ANY($1) AND NOT EXISTS(SELECT 1 FROM admin_capability_items newer WHERE newer.account_id=i.account_id AND newer.id>i.id)
		ORDER BY i.account_id FOR SHARE OF i,r`, pq.Array(publicationSortedIDs(missing)))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var folders, accounts []byte
		e := &service.CapabilityPublicationDeletedAccountEvidence{}
		if err = rows.Scan(&id, &e.ID, &e.FolderID, &e.ConfigFingerprint, &folders, &accounts); err != nil {
			_ = rows.Close()
			return err
		}
		if err = json.Unmarshal(folders, &e.RunFolderIDs); err != nil {
			_ = rows.Close()
			return err
		}
		if err = json.Unmarshal(accounts, &e.RunAccountIDs); err != nil {
			_ = rows.Close()
			return err
		}
		d := snap.DeletedAccounts[id]
		if d == nil || !d.Missing {
			_ = rows.Close()
			return service.ErrCapabilityPublicationConflict
		}
		d.FolderID, d.Evidence = e.FolderID, e
	}
	err = rows.Err()
	_ = rows.Close()
	return err
}

func publicationRecordAccountBinding(snap *service.CapabilityPublicationSnapshot, accountID, groupID int64, priority int) error {
	if as := snap.Accounts[accountID]; as != nil {
		as.Bindings[groupID] = priority
		return nil
	}
	if d := snap.DeletedAccounts[accountID]; d != nil {
		d.Bindings[groupID] = priority
		return nil
	}
	return service.ErrCapabilityPublicationConflict
}
