package service

import (
	"context"
	"sync"
)

// Only active observations register here. Identity edits cancel their work,
// without changing or replaying a business request that has already been sent.
var borrowPolicyObservers = struct {
	sync.Mutex
	next     uint64
	accounts map[int64]map[uint64]context.CancelFunc
}{accounts: make(map[int64]map[uint64]context.CancelFunc)}

func borrowPolicyContext(parent context.Context, accountID int64, revision uint64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	borrowPolicyObservers.Lock()
	borrowPolicyObservers.next++
	id := borrowPolicyObservers.next
	if borrowPolicyObservers.accounts[accountID] == nil {
		borrowPolicyObservers.accounts[accountID] = make(map[uint64]context.CancelFunc)
	}
	borrowPolicyObservers.accounts[accountID][id] = cancel
	if currentCodexFingerprintPolicyForAccount(&Account{ID: accountID}).revision != revision {
		cancel()
	}
	borrowPolicyObservers.Unlock()
	return ctx, func() {
		cancel()
		borrowPolicyObservers.Lock()
		delete(borrowPolicyObservers.accounts[accountID], id)
		if len(borrowPolicyObservers.accounts[accountID]) == 0 {
			delete(borrowPolicyObservers.accounts, accountID)
		}
		borrowPolicyObservers.Unlock()
	}
}

func cancelBorrowPolicyObservers(accountID int64) {
	borrowPolicyObservers.Lock()
	defer borrowPolicyObservers.Unlock()
	for id, callbacks := range borrowPolicyObservers.accounts {
		if accountID != 0 && id != accountID {
			continue
		}
		for _, cancel := range callbacks {
			cancel()
		}
	}
}
