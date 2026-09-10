package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Probe identity deliberately ignores mappings and group bindings. A management
// plan cannot: otherwise an applied idempotency key could be reused after an
// administrator removed a selector or changed a binding in another browser.
// This internal digest includes no authentication material and is not exposed
// as evidence or returned to public API clients.
func capabilityAccountPublicationRevision(account *Account) string {
	if account == nil {
		return ""
	}
	type binding struct {
		GroupID  int64 `json:"group_id"`
		Priority int   `json:"priority"`
	}
	groupIDs := append([]int64(nil), account.GroupIDs...)
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	bindings := make([]binding, 0, len(account.AccountGroups))
	for _, group := range account.AccountGroups {
		bindings = append(bindings, binding{GroupID: group.GroupID, Priority: group.Priority})
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].GroupID != bindings[j].GroupID {
			return bindings[i].GroupID < bindings[j].GroupID
		}
		return bindings[i].Priority < bindings[j].Priority
	})
	value := struct {
		AccountID      int64     `json:"account_id"`
		GroupIDs       []int64   `json:"group_ids"`
		Bindings       []binding `json:"bindings"`
		ModelMapping   any       `json:"model_mapping"`
		CompactMapping any       `json:"compact_model_mapping"`
	}{account.ID, groupIDs, bindings, account.Credentials["model_mapping"], account.Credentials["compact_model_mapping"]}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
