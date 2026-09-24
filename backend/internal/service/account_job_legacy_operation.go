package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Historical encrypted operation envelope; no new submissions use this format.
type PluginJobPayload struct {
	PluginID  int64                      `json:"plugin_id"`
	Operation string                     `json:"operation"`
	Items     map[string]json.RawMessage `json:"items"`
}

var ErrLegacyAccountJobUnsupported = errors.New("stored account operation has no native executor")

type LegacyCodexAccountJobIntent struct {
	PluginID  int64
	AccountID int64
	Operation string
	Model     string
	Force     bool
}

// The old host hashed the frozen account ID, one NUL, and the canonical raw
// operation object before adding operation_id. Preserve that exact 64-byte
// hexadecimal contract; a truncated or mismatched action never selects a task.
func DecodeLegacyCodexAccountJob(metadata, payload json.RawMessage, item AccountJobItem) (*LegacyCodexAccountJobIntent, error) {
	var request PluginJobPayload
	if json.Unmarshal(payload, &request) != nil || request.PluginID <= 0 {
		return nil, ErrAccountJobInvalidMetadata
	}
	if request.Operation != "harvest" && request.Operation != "stop" {
		return nil, ErrLegacyAccountJobUnsupported
	}
	owner, err := AccountJobPluginExecution(metadata)
	if err != nil || owner.ID != request.PluginID {
		return nil, ErrAccountJobInvalidMetadata
	}
	if item.TargetAccountID == nil || *item.TargetAccountID <= 0 || !accountViewDigestPattern.MatchString(item.Action) {
		return nil, ErrAccountJobInvalidMetadata
	}
	raw, ok := request.Items[item.Action]
	if !ok {
		return nil, ErrAccountJobInvalidMetadata
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	id := *item.TargetAccountID
	digest := sha256.Sum256(append([]byte(strconv.FormatInt(id, 10)+"\x00"), canonical...))
	if hex.EncodeToString(digest[:]) != item.Action {
		return nil, ErrAccountJobInvalidMetadata
	}
	if rawID, present := fields["account_id"]; present {
		var declared int64
		if json.Unmarshal(rawID, &declared) != nil || declared != id {
			return nil, ErrAccountJobInvalidMetadata
		}
	}
	var model string
	if json.Unmarshal(fields["model"], &model) != nil || strings.TrimSpace(model) == "" {
		return nil, ErrAccountJobInvalidMetadata
	}
	var force bool
	if rawForce, present := fields["force"]; present && json.Unmarshal(rawForce, &force) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	return &LegacyCodexAccountJobIntent{PluginID: request.PluginID, AccountID: id, Operation: request.Operation, Model: strings.TrimSpace(model), Force: force}, nil
}
