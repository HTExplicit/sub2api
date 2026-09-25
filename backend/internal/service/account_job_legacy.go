package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// These identities decode existing encrypted jobs. They never select or start
// an installation, stamp a new request, or authorize a different account set.
type AccountJobLegacyOwner struct {
	ID         int64
	Generation int64
}

var (
	ErrAccountJobPluginUnavailable     = errors.New("stored account job operation is unavailable")
	ErrAccountViewInvalid              = ErrAccountJobInvalidMetadata
	ErrAccountViewUnavailable          = ErrAccountJobInvalidMetadata
	ErrAccountViewScope                = ErrAccountJobInvalidMetadata
	ErrAccountViewUnsupportedOperation = ErrAccountJobInvalidMetadata
	accountViewIDPattern               = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	accountViewDigestPattern           = regexp.MustCompile(`^[a-f0-9]{64}$`)
	legacyPluginIDPattern              = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)
)

func AccountJobPluginExecution(metadata json.RawMessage) (AccountJobLegacyOwner, error) {
	var owner struct {
		ID         int64 `json:"plugin_id"`
		Generation int64 `json:"plugin_generation"`
	}
	if len(metadata) > 0 && json.Unmarshal(metadata, &owner) != nil {
		return AccountJobLegacyOwner{}, ErrAccountJobInvalidMetadata
	}
	if owner.ID < 0 || owner.Generation < 0 || (owner.ID == 0 && owner.Generation != 0) {
		return AccountJobLegacyOwner{}, ErrAccountJobInvalidMetadata
	}
	return AccountJobLegacyOwner{ID: owner.ID, Generation: owner.Generation}, nil
}

type AccountJobViewMetadata struct {
	extensionv1.AccountViewIdentityV1
	RuntimeGeneration     int64  `json:"runtime_generation"`
	PolicyRevision        int64  `json:"policy_revision"`
	NormalizedQueryDigest string `json:"normalized_query_digest"`
}

func ValidateAccountViewIdentity(identity extensionv1.AccountViewIdentityV1) error {
	if identity.Version != 1 || identity.PluginID <= 0 || !legacyPluginIDPattern.MatchString(identity.PluginKey) || len(identity.PluginKey) > 160 || !accountViewIDPattern.MatchString(identity.ViewID) || !accountViewIDPattern.MatchString(identity.PresetID) || !accountViewDigestPattern.MatchString(identity.PackageSHA256) || !accountViewDigestPattern.MatchString(identity.ViewDefinitionDigest) {
		return ErrAccountViewInvalid
	}
	return nil
}

func boundedAccountViewStrings(values []string, limit int) error {
	if len(values) > limit {
		return ErrAccountViewInvalid
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || len(value) > 64 || strings.TrimSpace(value) != value || seen[value] || strings.ContainsAny(value, "\x00\r\n") {
			return ErrAccountViewInvalid
		}
		seen[value] = true
	}
	return nil
}

func NormalizeAccountViewQuery(query extensionv1.AccountViewQueryV1) (extensionv1.AccountViewQueryV1, error) {
	for _, values := range [][]string{query.Platforms, query.Types, query.Statuses, query.Plans} {
		if err := boundedAccountViewStrings(values, 32); err != nil {
			return query, err
		}
	}
	if len(query.Search) > 100 || len(query.PrivacyMode) > 64 || query.GroupID < AccountListGroupUngrouped {
		return query, ErrAccountViewInvalid
	}
	query.Search = strings.TrimSpace(query.Search)
	if query.SortBy != "" && !slices.Contains([]string{"id", "name", "platform", "type", "status", "schedulable", "priority", "concurrency", "rate_multiplier", "upstream_billing_rate", "last_used_at", "created_at", "updated_at", "expires_at"}, query.SortBy) {
		return query, ErrAccountViewInvalid
	}
	if query.SortOrder != "" && query.SortOrder != "asc" && query.SortOrder != "desc" {
		return query, ErrAccountViewInvalid
	}
	for _, values := range [][]int64{query.Tags, query.AccountIDs} {
		if len(values) > 3200 {
			return query, ErrAccountViewInvalid
		}
		for _, id := range values {
			if id <= 0 {
				return query, ErrAccountViewInvalid
			}
		}
	}
	for sentinel, values := range map[string][]string{"direct": query.Proxies, "uncategorized": query.Folders} {
		if len(values) > 3200 {
			return query, ErrAccountViewInvalid
		}
		for _, value := range values {
			if value == sentinel {
				continue
			}
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
				return query, ErrAccountViewInvalid
			}
		}
	}
	normalizeStrings := func(values []string) []string {
		out := slices.Clone(values)
		slices.Sort(out)
		return slices.Compact(out)
	}
	normalizeIDs := func(values []int64) []int64 {
		out := slices.Clone(values)
		slices.Sort(out)
		return slices.Compact(out)
	}
	query.Platforms, query.Types, query.Statuses, query.Plans = normalizeStrings(query.Platforms), normalizeStrings(query.Types), normalizeStrings(query.Statuses), normalizeStrings(query.Plans)
	query.Proxies, query.Folders = normalizeStrings(query.Proxies), normalizeStrings(query.Folders)
	query.Tags, query.AccountIDs = normalizeIDs(query.Tags), normalizeIDs(query.AccountIDs)
	return query, nil
}

func AccountJobViewExecution(metadata json.RawMessage) (*AccountJobViewMetadata, error) {
	var envelope struct {
		View json.RawMessage `json:"account_view"`
	}
	if len(metadata) > 0 && json.Unmarshal(metadata, &envelope) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	if len(envelope.View) == 0 || string(envelope.View) == "null" {
		return nil, nil
	}
	var view AccountJobViewMetadata
	decoder := json.NewDecoder(bytes.NewReader(envelope.View))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil || ValidateAccountViewIdentity(view.AccountViewIdentityV1) != nil || view.RuntimeGeneration <= 0 || view.PolicyRevision < 0 || !accountViewDigestPattern.MatchString(view.NormalizedQueryDigest) {
		return nil, ErrAccountJobInvalidMetadata
	}
	return &view, nil
}

func accountViewQueryDigest(query extensionv1.AccountViewQueryV1) string {
	raw, _ := json.Marshal(query)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func AccountJobViewIdentityEqual(left, right json.RawMessage) bool {
	a, aErr := AccountJobViewExecution(left)
	b, bErr := AccountJobViewExecution(right)
	if aErr != nil || bErr != nil {
		return false
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.AccountViewIdentityV1 == b.AccountViewIdentityV1 && a.NormalizedQueryDigest == b.NormalizedQueryDigest
}

func accountJobPayloadView(payload json.RawMessage) (*extensionv1.AccountViewContextV1, error) {
	var envelope struct {
		View json.RawMessage `json:"view_context"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	if len(envelope.View) == 0 || string(envelope.View) == "null" {
		return nil, nil
	}
	var view extensionv1.AccountViewContextV1
	decoder := json.NewDecoder(bytes.NewReader(envelope.View))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	query, err := NormalizeAccountViewQuery(view.Query)
	if err != nil {
		return nil, err
	}
	view.Query = query
	return &view, nil
}

func accountViewJobTargets(kind string, payload json.RawMessage, targets []*int64) ([]int64, error) {
	var ids []int64
	for _, target := range targets {
		if target == nil {
			return nil, ErrAccountViewScope
		}
		ids = append(ids, *target)
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrAccountViewScope
		}
	}
	if len(ids) == 0 {
		return nil, ErrAccountViewScope
	}
	return ids, nil
}

// ValidateRecordedAccountJob verifies original view metadata against its saved
// encrypted query. The query is compared only; execution uses persisted IDs.
func ValidateRecordedAccountJob(job *AccountJob, payload json.RawMessage) error {
	if job == nil || !json.Valid(payload) {
		return ErrAccountJobInvalidMetadata
	}
	if _, err := AccountJobPluginExecution(job.Metadata); err != nil {
		return err
	}
	return validateRecordedAccountJobPayload(job.Metadata, payload)
}

func validateRecordedAccountJobPayload(metadata, payload json.RawMessage) error {
	stored, err := AccountJobViewExecution(metadata)
	if err != nil {
		return err
	}
	if stored == nil {
		var envelope struct {
			View json.RawMessage `json:"view_context"`
		}
		if json.Unmarshal(payload, &envelope) == nil && len(envelope.View) > 0 && string(envelope.View) != "null" {
			return ErrAccountJobInvalidMetadata
		}
		return nil
	}
	request, err := accountJobPayloadView(payload)
	if err != nil || request == nil || stored.AccountViewIdentityV1 != request.AccountViewIdentityV1 || stored.NormalizedQueryDigest != accountViewQueryDigest(request.Query) {
		return ErrAccountJobInvalidMetadata
	}
	return nil
}

func bindRecordedAccountJobView(ctx context.Context, metadata, payload json.RawMessage, _ bool) (context.Context, func(), error) {
	if err := validateRecordedAccountJobPayload(metadata, payload); err != nil {
		return nil, nil, err
	}
	return ctx, func() {}, nil
}

func ValidateRecordedAccountJobTargets(job *AccountJob, payload json.RawMessage, items []AccountJobItem) error {
	if err := ValidateRecordedAccountJob(job, payload); err != nil {
		return err
	}
	stored, _ := AccountJobViewExecution(job.Metadata)
	if stored == nil {
		return nil
	}
	targets := make([]*int64, 0, len(items))
	for _, item := range items {
		targets = append(targets, item.TargetAccountID)
	}
	_, err := accountViewJobTargets(job.Kind, payload, targets)
	return err
}
