package service

import (
	"fmt"
	"time"
)

// DeletedAccounts is a server-owned observation, never caller-supplied deletion
// authority. Soft-deleted rows retain their actual folder; a physically missing
// row requires matching, latest ledger provenance before it may be cleaned up.
// No credentials are loaded into this snapshot or modified by its patches.
type CapabilityPublicationDeletedAccountSnapshot struct {
	Missing         bool
	DeletedAt       *time.Time
	FolderID        *int64
	Platform        string
	ProviderProfile string
	ParentAccountID *int64
	Bindings        map[int64]int
	Evidence        *CapabilityPublicationDeletedAccountEvidence
}

type CapabilityPublicationDeletedAccountEvidence struct {
	ID                int64
	FolderID          *int64
	ConfigFingerprint string
	RunFolderIDs      []int64
	RunAccountIDs     []int64
}

func publicationValidateSnapshotScope(snap *CapabilityPublicationSnapshot) error {
	for _, id := range snap.Request.Scope.AccountIDs {
		if as := snap.Accounts[id]; as != nil && as.Account != nil {
			if as.Account.ManagementFolderID == nil || !publicationHasID(snap.Request.Scope.FolderIDs, *as.Account.ManagementFolderID) {
				return ErrCapabilityPublicationConflict
			}
			continue
		}
		if !publicationDeletedAccountInScope(snap, id) {
			return ErrCapabilityPublicationConflict
		}
	}
	for _, id := range snap.Request.DetachAccountIDs {
		// Preserve the existing explicit detachment authority for live accounts.
		// A deleted row cannot use that path to bypass missing folder provenance.
		if as := snap.Accounts[id]; as != nil && as.Account != nil {
			continue
		}
		if !publicationDeletedAccountInScope(snap, id) {
			return ErrCapabilityPublicationConflict
		}
	}
	return nil
}

func publicationDeletedAccountInScope(snap *CapabilityPublicationSnapshot, id int64) bool {
	d := snap.DeletedAccounts[id]
	if d == nil || snap.Accounts[id] != nil || d.FolderID == nil || !publicationHasID(snap.Request.Scope.FolderIDs, *d.FolderID) ||
		d.ParentAccountID != nil || d.Platform == PlatformCindy || d.ProviderProfile == "cindy" {
		return false
	}
	if !d.Missing {
		return d.DeletedAt != nil
	}
	// A favorable old observation is not enough: the repository supplies only
	// the latest item for this account, including its original run scope. Every
	// retained reference must match that account configuration; ambiguity fails
	// closed instead of selecting an older, in-scope observation.
	e := d.Evidence
	if d.DeletedAt != nil || e == nil || e.ID <= 0 || e.FolderID == nil || *e.FolderID != *d.FolderID || e.ConfigFingerprint == "" ||
		!publicationHasID(e.RunFolderIDs, *e.FolderID) || !publicationHasID(e.RunAccountIDs, id) {
		return false
	}
	found := false
	for _, gs := range snap.Groups {
		for _, route := range gs.Group.ManagedModelRoutes.Routes {
			for _, branch := range ManagedModelRouteBranches(route) {
				for _, member := range branch.Accounts {
					if member.AccountID != id {
						continue
					}
					if branch.TargetPlatform == PlatformCindy || member.AccountFingerprint != e.ConfigFingerprint {
						return false
					}
					found = true
				}
			}
		}
	}
	return found
}

func publicationPlanDeletedAccountPatch(snap *CapabilityPublicationSnapshot, id int64, ap *CapabilityPublicationAccountPatch, plan *CapabilityPublicationPlan) error {
	if !publicationRemovalInScope(snap, id) || !publicationDeletedAccountInScope(snap, id) || len(ap.ModelMapping) > 0 || len(ap.AddGroupIDs) > 0 || ap.Schedulable != nil {
		return ErrCapabilityPublicationConflict
	}
	d := snap.DeletedAccounts[id]
	ap.RemoveGroupIDs = publicationUniqueIDs(ap.RemoveGroupIDs)
	for _, gid := range ap.RemoveGroupIDs {
		gs := snap.Groups[gid]
		if gs == nil {
			return ErrCapabilityPublicationConflict
		}
		if _, bound := gs.Bindings[id]; !bound {
			return ErrCapabilityPublicationConflict
		}
		if _, bound := d.Bindings[gid]; !bound {
			return ErrCapabilityPublicationConflict
		}
	}
	// Removing a dead route does not grant permission to rewrite a deleted
	// account's credentials. The group plan already records the exact route
	// subtraction; only genuinely surviving public bindings need SQL writes.
	ap.RemoveSelectors = nil
	if len(ap.RemoveGroupIDs) == 0 {
		return nil
	}
	plan.Changes = append(plan.Changes, CapabilityPublicationChange{
		Kind: "bindings", AccountID: id, Label: fmt.Sprintf("deleted account %d", id),
		Before:      publicationBindingView(d.Bindings),
		After:       map[string]any{"add_group_ids": ap.AddGroupIDs, "remove_group_ids": ap.RemoveGroupIDs, "preserve_existing_priorities": true},
		EvidenceIDs: []int64{},
	})
	plan.Accounts = append(plan.Accounts, *ap)
	return nil
}
