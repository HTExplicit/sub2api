package service

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// Stored only in the existing encrypted job payload, after computing the
// caller's original operation hash. It is never a caller-selectable privilege.
type AccountJobEditSnapshot struct {
	StateSHA256 string                       `json:"state_sha256"`
	Profile     *AccountEditProfileReference `json:"profile,omitempty"`
}

const accountJobEditSnapshotsKey = "_host_account_edit"

type accountJobEditAccountsKey struct{}

// The native handler supplies rows it read for the frozen target IDs. This
// keeps ordinary account submissions independent of the extension runtime.
func WithAccountJobEditAccounts(ctx context.Context, accounts []*Account) context.Context {
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	return context.WithValue(ctx, accountJobEditAccountsKey{}, byID)
}

func captureNativeAccountJobEdits(ctx context.Context, items []AccountJobItemSeed, accounts map[int64]*Account, credentials, extra map[string]any) (map[string]AccountJobEditSnapshot, error) {
	result := map[string]AccountJobEditSnapshot{}
	for _, item := range items {
		if item.TargetAccountID == nil || accounts[*item.TargetAccountID] == nil {
			return nil, ErrAccountEditStateChanged
		}
		account := accounts[*item.TargetAccountID]
		if !hasCanonicalCindyProviderIdentity(account) {
			continue
		}
		input := &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Credentials: maps.Clone(credentials), Extra: maps.Clone(extra)}
		settings, err := normalizeBulkOpenAISettings(input)
		if err != nil {
			return nil, err
		}
		if _, err := validateBulkOpenAISettingsTargets(input, settings, map[int64]*Account{account.ID: account}); err != nil {
			return nil, err
		}
		bound, release, err := PrepareAccountEdit(ctx, account, credentials, extra, nil)
		if err != nil {
			return nil, err
		}
		snapshot := AccountJobEditSnapshot{StateSHA256: AccountEditStateDigest(account)}
		if edit, present := AccountEditFromContext(bound); present && edit.changed {
			snapshot.Profile = nativeAccountEditProfile(edit.runtime)
		}
		release()
		result[strconv.FormatInt(account.ID, 10)] = snapshot
	}
	return result, nil
}

func accountJobPayloadWithEdit(ctx context.Context, kind string, payload json.RawMessage, items []AccountJobItemSeed) (json.RawMessage, error) {
	if kind != AccountJobKindBulkUpdate {
		return payload, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || fields == nil {
		return nil, ErrAccountEditInvalid
	}
	if _, supplied := fields[accountJobEditSnapshotsKey]; supplied {
		return nil, ErrAccountEditInvalid
	}
	var input struct {
		Credentials map[string]any `json:"credentials"`
		Extra       map[string]any `json:"extra"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return nil, ErrAccountEditInvalid
	}
	if !HasAccountEditOwnedInput(input.Credentials, input.Extra) {
		return payload, nil
	}
	var snapshots map[string]AccountJobEditSnapshot
	var err error
	if accounts, ok := ctx.Value(accountJobEditAccountsKey{}).(map[int64]*Account); ok {
		snapshots, err = captureNativeAccountJobEdits(ctx, items, accounts, input.Credentials, input.Extra)
	} else {
		return nil, ErrAccountEditUnavailable
	}

	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return payload, nil
	}
	fields[accountJobEditSnapshotsKey], err = json.Marshal(snapshots)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// Prepare once before the worker's existing Cindy runner obtains any account
// locks. The original action owner, target IDs, hash, TTL and view are retained.
func PrepareAccountJobEdit(ctx context.Context, raw json.RawMessage, account *Account, credentials, extra map[string]any) (context.Context, func(), error) {
	if account == nil || account.ID <= 0 {
		return nil, nil, ErrAccountEditInvalid
	}
	var envelope struct {
		Snapshots map[string]AccountJobEditSnapshot `json:"_host_account_edit"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, nil, ErrAccountEditInvalid
	}
	snapshot, exists := envelope.Snapshots[strconv.FormatInt(account.ID, 10)]
	if exists {
		if !hasCanonicalCindyProviderIdentity(account) || snapshot.StateSHA256 != AccountEditStateDigest(account) {
			return nil, nil, ErrAccountEditStateChanged
		}
	} else {
		if hasCanonicalCindyProviderIdentity(account) && HasAccountEditOwnedInput(credentials, extra) {
			// Old queued native requests have no captured edit preconditions.
			// They may retain an exactly unchanged value, but cannot perform a
			// real owned write without a newly frozen submission. The no-op plan
			// still compares state again after the worker obtains its row lock.
			_, changed, err := accountEditDesired(account, credentials, extra, nil, nil)
			if err != nil || changed {
				return nil, nil, ErrAccountEditStateChanged
			}
			return PrepareAccountEdit(ctx, account, credentials, extra, nil)
		}
		return ctx, func() {}, nil
	}
	bound, release, err := PrepareAccountEdit(ctx, account, credentials, extra, nil)
	if err != nil {
		return nil, nil, err
	}
	edit, _ := AccountEditFromContext(bound)
	if edit != nil && edit.changed {
		// Historical encrypted jobs retain their original state digest and
		// frozen target. Their retired installation is no longer an executor.
		if snapshot.Profile == nil {
			release()
			return nil, nil, ErrAccountEditUnavailable
		}
		if snapshot.Profile.Native && snapshot.Profile.PolicySHA256 != nativeAccountEditProfile(edit.runtime).PolicySHA256 {
			release()
			return nil, nil, ErrAccountEditUnavailable
		}

	} else if snapshot.Profile != nil {
		release()
		return nil, nil, ErrAccountEditStateChanged
	}
	return bound, release, nil
}

type accountEditBulkPreparedKey struct{}

// Direct service callers and durable workers share the same per-row fence.
// Existing bulk semantics are per-target; mixed basic+owned fields commit in
// one target transaction, never as an earlier unguarded JSONB patch.
func (s *adminServiceImpl) runBulkAccountEdits(ctx context.Context, input *BulkUpdateAccountsInput, targets map[int64]*Account) (*BulkUpdateAccountsResult, bool, error) {
	// Check the captured identity before classifying the newly read row. A
	// concurrent identity change must not turn a fenced Cindy edit into an
	// unguarded ordinary merge (including a payload restoring the old endpoint).
	if edit, bound := AccountEditFromContext(ctx); bound {
		if len(input.AccountIDs) != 1 || edit.AccountID != input.AccountIDs[0] || targets[edit.AccountID] == nil ||
			edit.StateSHA256 != AccountEditStateDigest(targets[edit.AccountID]) || edit.intentSHA256 != accountEditIntentDigest(input.Credentials, input.Extra, nil) {
			return nil, true, ErrAccountEditStateChanged
		}
	}
	if !HasAccountEditOwnedInput(input.Credentials, input.Extra) {
		return nil, false, nil
	}
	canonical := false
	for _, account := range targets {
		canonical = canonical || hasCanonicalCindyProviderIdentity(account)
	}
	if !canonical {
		return nil, false, nil
	}
	if prepared, _ := ctx.Value(accountEditBulkPreparedKey{}).(bool); prepared || dbent.TxFromContext(ctx) != nil {
		for _, id := range input.AccountIDs {
			account := targets[id]
			if hasCanonicalCindyProviderIdentity(account) {
				if _, err := AccountEditOwnedForUpdate(ctx, account, input.Credentials, input.Extra, nil); err != nil {
					return nil, true, err
				}
			}
		}
		if tx := dbent.TxFromContext(ctx); tx != nil {
			retainBoundAccountEditLease(ctx, tx)
		}
		return nil, false, nil
	}
	if s.cindyAccountMutations == nil {
		return nil, true, ErrAccountEditUnavailable
	}
	result := &BulkUpdateAccountsResult{SuccessIDs: []int64{}, FailedIDs: []int64{}, Results: []BulkUpdateAccountResult{}}
	for _, id := range input.AccountIDs {
		account := targets[id]
		if account == nil {
			return nil, true, ErrAccountNotFound
		}
		bound, release, err := PrepareAccountEdit(ctx, account, input.Credentials, input.Extra, nil)
		if err != nil {
			return nil, true, err
		}
		copy := *input
		copy.AccountIDs, copy.Filters = []int64{id}, nil
		copy.Credentials, copy.Extra = maps.Clone(input.Credentials), maps.Clone(input.Extra)
		apply := func(txCtx context.Context) (*Account, error) {
			part, err := s.BulkUpdateAccounts(context.WithValue(txCtx, accountEditBulkPreparedKey{}, true), &copy)
			if err != nil {
				return nil, err
			}
			if part == nil || part.Failed != 0 || part.Success != 1 {
				return nil, ErrAccountEditUnavailable
			}
			return s.accountRepo.GetByID(txCtx, id)
		}
		if hasCanonicalCindyProviderIdentity(account) {
			_, err = s.cindyAccountMutations.Run(bound, id, apply)
		} else {
			_, err = apply(bound)
		}
		release()
		if err != nil {
			return nil, true, err
		}
		result.Success++
		result.SuccessIDs = append(result.SuccessIDs, id)
		result.Results = append(result.Results, BulkUpdateAccountResult{AccountID: id, Success: true})
	}
	return result, true, nil
}
