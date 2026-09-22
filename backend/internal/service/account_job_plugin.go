package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var ErrAccountJobPluginUnavailable = errors.New("account job plugin is unavailable or has changed")

// Account jobs are administrative operations. Image and probe job storage has
// its own execution contract and does not pass through this metadata envelope.
func AccountJobPluginExecution(metadata json.RawMessage) (PluginExecution, error) {
	var owner struct {
		ID         int64 `json:"plugin_id"`
		Generation int64 `json:"plugin_generation"`
	}
	if len(metadata) > 0 && json.Unmarshal(metadata, &owner) != nil {
		return PluginExecution{}, ErrAccountJobInvalidMetadata
	}
	if owner.ID < 0 || owner.Generation < 0 || (owner.ID == 0 && owner.Generation != 0) {
		return PluginExecution{}, ErrAccountJobInvalidMetadata
	}
	return PluginExecution{ID: owner.ID, Generation: owner.Generation}, nil
}

func stampAccountJobPlugin(metadata json.RawMessage, execution PluginExecution) (json.RawMessage, error) {
	owner, err := AccountJobPluginExecution(metadata)
	if err != nil {
		return nil, err
	}
	if execution.ID <= 0 || execution.Generation <= 0 || (owner.ID > 0 && owner.ID != execution.ID) {
		return nil, ErrAccountJobPluginUnavailable
	}
	fields := map[string]json.RawMessage{}
	if len(metadata) > 0 && json.Unmarshal(metadata, &fields) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["plugin_id"], _ = json.Marshal(execution.ID)
	fields["plugin_generation"], _ = json.Marshal(execution.Generation)
	return json.Marshal(fields)
}

type accountJobPluginBinder interface {
	BindAccountJobExecution(context.Context, int64, int64, ...string) (context.Context, func(), error)
}

func bindAccountJobPlugin(ctx context.Context, owner PluginExecution, kind string) (context.Context, func(), error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	binder, ok := provider.invoker.(accountJobPluginBinder)
	if !ok {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	return binder.BindAccountJobExecution(ctx, owner.ID, owner.Generation, kind)
}

// The separate shared database lease lasts until the host task actually exits,
// including its transaction cleanup. Killing only the old plugin process must
// not let an update overtake host IO that is still finishing cancellation.
func (m *PluginManager) BindAccountJobExecution(ctx context.Context, id, expectedGeneration int64, kinds ...string) (context.Context, func(), error) {
	if m == nil || m.repo == nil {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	current, err := m.repo.GetByID(ctx, id)
	if err == nil && current != nil && PluginExpectedPackage(ctx) != "" && PluginExpectedPackage(ctx) != current.PackageSHA256 {
		return nil, nil, ErrPluginStateChanged
	}
	if err != nil || current == nil || current.State != PluginStateEnabled || (expectedGeneration > 0 && current.RuntimeGeneration != expectedGeneration) {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" || !samePluginRuntime(current, registry.installations[id]) {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	admin := false
	for _, binding := range current.Bindings {
		admin = admin || (binding.Enabled && binding.Capability == extensionv1.CapabilityAdmin)
	}
	runtime := registry.runtimes[id]
	if !admin || runtime == nil || runtime.client == nil || runtime.client.Exited() || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
	if err != nil {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	bound = WithPluginExecution(bound, current)
	if len(kinds) > 0 && isCindyCleanupAccountJob(kinds[0]) {
		if err := m.ValidateResourcePolicy(bound, CindyCleanupResourcePolicy(), nil, true); err != nil {
			release()
			return nil, nil, ErrAccountJobPluginUnavailable
		}
	}
	if len(kinds) > 0 && (kinds[0] == AccountJobKindImportData || kinds[0] == AccountJobKindImportCodex || kinds[0] == AccountJobKindBatchCreate) {
		if err := m.ValidateResourceAccounts(bound, extensionv1.CapabilityAdmin, nil, true); err != nil {
			release()
			return nil, nil, ErrAccountJobPluginUnavailable
		}
	}
	return bound, release, nil
}

func isCindyCleanupAccountJob(kind string) bool {
	return kind == AccountJobKindCindyConfirmedCleanup || kind == AccountJobKindCindyBannedCleanup
}

// The generic RPC back-callback contract has no origin-view proof. Keep that
// existing background channel unchanged and require named host jobs for a view.
func validateAccountViewJobKind(ctx context.Context, kind string, metadata json.RawMessage) error {
	if kind != AccountJobKindExtensionOperation {
		return nil
	}
	if _, bound := AccountViewFromContext(ctx); bound {
		return ErrAccountViewUnsupportedOperation
	}
	view, err := AccountJobViewExecution(metadata)
	if err != nil {
		return err
	}
	if view != nil {
		return ErrAccountViewUnsupportedOperation
	}
	return nil
}

// Public metadata contains only identity/fences and a digest. The full query is
// in the existing encrypted payload. Action owner and origin view are separate.
type AccountJobViewMetadata struct {
	extensionv1.AccountViewIdentityV1
	RuntimeGeneration     int64  `json:"runtime_generation"`
	PolicyRevision        int64  `json:"policy_revision"`
	NormalizedQueryDigest string `json:"normalized_query_digest"`
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

func AccountJobExecutionFences(metadata json.RawMessage) ([]PluginExecutionFence, error) {
	primary, err := AccountJobPluginExecution(metadata)
	if err != nil {
		return nil, err
	}
	view, err := AccountJobViewExecution(metadata)
	if err != nil {
		return nil, err
	}
	var fences []PluginExecutionFence
	if primary.ID > 0 {
		fences = append(fences, PluginExecutionFence{ID: primary.ID, Generation: primary.Generation, Primary: true})
	}
	if view != nil {
		fences = append(fences, PluginExecutionFence{ID: view.PluginID, Generation: view.RuntimeGeneration, PluginKey: view.PluginKey, PackageSHA256: view.PackageSHA256, PolicyRevision: view.PolicyRevision, OriginView: true})
	}
	return mergePluginExecutionFences(fences)
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

func accountJobPayloadWithView(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		var probe struct {
			View json.RawMessage `json:"view_context"`
		}
		// Non-object native payloads keep their pre-existing hash/validation path.
		if json.Unmarshal(payload, &probe) == nil && len(probe.View) > 0 && string(probe.View) != "null" {
			return nil, ErrAccountViewUnavailable
		}
		return payload, nil
	}
	provided, err := accountJobPayloadView(payload)
	if err != nil {
		return nil, err
	}
	if provided != nil && !accountViewJSONEqual(provided, view.Request) {
		return nil, ErrAccountViewInvalid
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(payload, &fields) != nil || fields == nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	fields["view_context"], _ = json.Marshal(view.Request)
	return json.Marshal(fields)
}

func stampAccountJobView(ctx context.Context, metadata json.RawMessage) (json.RawMessage, error) {
	old, err := AccountJobViewExecution(metadata)
	if err != nil {
		return nil, err
	}
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		if old != nil {
			return nil, ErrAccountViewUnavailable
		}
		return metadata, nil
	}
	if err := ValidateAccountViewFresh(ctx); err != nil {
		return nil, err
	}
	current := AccountJobViewMetadata{AccountViewIdentityV1: view.Request.AccountViewIdentityV1, RuntimeGeneration: view.Execution.Generation, PolicyRevision: view.installation.Revision, NormalizedQueryDigest: accountViewQueryDigest(view.Request.Query)}
	if old != nil && (old.AccountViewIdentityV1 != current.AccountViewIdentityV1 || old.NormalizedQueryDigest != current.NormalizedQueryDigest) {
		return nil, ErrAccountJobIdempotencyConflict
	}
	fields := map[string]json.RawMessage{}
	if len(metadata) > 0 && json.Unmarshal(metadata, &fields) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["account_view"], _ = json.Marshal(current)
	return json.Marshal(fields)
}

func (m *PluginManager) BindAccountJobView(ctx context.Context, metadata, payload json.RawMessage, allowNewGeneration bool) (context.Context, func(), error) {
	stored, err := AccountJobViewExecution(metadata)
	if err != nil {
		return nil, nil, err
	}
	if stored == nil {
		if _, err := accountJobPayloadWithView(context.Background(), payload); err != nil {
			return nil, nil, err
		}
		return ctx, func() {}, nil
	}
	request, err := accountJobPayloadView(payload)
	if err != nil {
		return nil, nil, err
	}
	if request == nil || stored.AccountViewIdentityV1 != request.AccountViewIdentityV1 || stored.NormalizedQueryDigest != accountViewQueryDigest(request.Query) {
		return nil, nil, ErrAccountViewUnavailable
	}
	if caller, bound := AccountViewFromContext(ctx); bound && !accountViewJSONEqual(caller.Request, request) {
		return nil, nil, ErrAccountJobIdempotencyConflict
	}
	bound, release, err := m.BindAccountViewRequest(ctx, *request)
	if err != nil {
		return nil, nil, err
	}
	view, _ := AccountViewFromContext(bound)
	if !allowNewGeneration && stored.RuntimeGeneration != view.Execution.Generation {
		release()
		return nil, nil, ErrAccountViewUnavailable
	}
	return bound, release, nil
}

func bindRecordedAccountJobView(ctx context.Context, metadata, payload json.RawMessage, allowNewGeneration bool) (context.Context, func(), error) {
	stored, err := AccountJobViewExecution(metadata)
	if err != nil {
		return nil, nil, err
	}
	if stored == nil {
		return ctx, func() {}, nil
	}
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrAccountViewUnavailable
	}
	binder, ok := provider.invoker.(interface {
		BindAccountJobView(context.Context, json.RawMessage, json.RawMessage, bool) (context.Context, func(), error)
	})
	if !ok {
		return nil, nil, ErrAccountViewUnavailable
	}
	return binder.BindAccountJobView(ctx, metadata, payload, allowNewGeneration)
}

func accountViewJobTargets(kind string, payload json.RawMessage, targets []*int64) ([]int64, error) {
	var ids []int64
	switch kind {
	case AccountJobKindDuplicateReview:
		var request struct {
			AccountIDs []int64 `json:"account_ids"`
		}
		if json.Unmarshal(payload, &request) != nil || len(request.AccountIDs) < 2 || len(request.AccountIDs) > AccountJobBatchSize {
			return nil, ErrAccountViewScope
		}
		ids = request.AccountIDs
	case AccountJobKindDuplicateMerge:
		var request struct {
			SurvivorAccountID int64   `json:"survivor_account_id"`
			LoserAccountIDs   []int64 `json:"loser_account_ids"`
		}
		if json.Unmarshal(payload, &request) != nil || request.SurvivorAccountID <= 0 || len(request.LoserAccountIDs) == 0 || len(request.LoserAccountIDs) >= AccountJobBatchSize || len(targets) != 1 || targets[0] == nil || *targets[0] != request.SurvivorAccountID {
			return nil, ErrAccountViewScope
		}
		ids = append([]int64{request.SurvivorAccountID}, request.LoserAccountIDs...)
	default:
		for _, target := range targets {
			if target == nil {
				return nil, ErrAccountViewScope
			}
			ids = append(ids, *target)
		}
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

func accountViewJobSeedTargets(kind string, payload json.RawMessage, seeds []AccountJobItemSeed) ([]int64, error) {
	targets := make([]*int64, 0, len(seeds))
	for _, seed := range seeds {
		targets = append(targets, seed.TargetAccountID)
	}
	return accountViewJobTargets(kind, payload, targets)
}

func validateAccountJobViewExecution(ctx context.Context, job *AccountJob, payload json.RawMessage, items []AccountJobItem) error {
	if err := validateAccountViewJobKind(ctx, job.Kind, job.Metadata); err != nil {
		return err
	}
	stored, err := AccountJobViewExecution(job.Metadata)
	if err != nil {
		return err
	}
	view, bound := AccountViewFromContext(ctx)
	if stored == nil {
		if bound {
			return ErrAccountViewUnavailable
		}
		return nil
	}
	if !bound || stored.AccountViewIdentityV1 != view.Request.AccountViewIdentityV1 || stored.RuntimeGeneration != view.Execution.Generation {
		return ErrAccountViewUnavailable
	}
	if err := ValidateAccountViewFresh(ctx); err != nil {
		return err
	}
	// These two named operations intentionally target their complete fixed domain,
	// not the originating view's active preset, visible rows or captured search.
	if isCindyCleanupAccountJob(job.Kind) {
		return nil
	}
	targets := make([]*int64, 0, len(items))
	for _, item := range items {
		targets = append(targets, item.TargetAccountID)
	}
	ids, err := accountViewJobTargets(job.Kind, payload, targets)
	if err != nil {
		return err
	}
	return ValidateAccountViewTargets(ctx, ids)
}

// The native merge continuation loads the original review selection/query only
// inside the host. It never exposes the encrypted request in public job metadata.
func (s *AccountJobService) AccountViewFromReviewJob(ctx context.Context, jobID, actorID int64) (*extensionv1.AccountViewContextV1, []int64, error) {
	job, err := s.repo.Get(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	if job == nil || job.Kind != AccountJobKindDuplicateReview || job.CreatedBy != actorID || job.Status != AccountJobStatusSucceeded {
		return nil, nil, ErrAccountViewScope
	}
	cipher, expires, err := s.repo.Payload(ctx, jobID)
	if err != nil || cipher == "" || time.Now().UTC().After(expires) {
		return nil, nil, ErrAccountJobPayloadExpired
	}
	plaintext, err := s.encryptor.Decrypt(cipher)
	if err != nil {
		return nil, nil, ErrAccountJobPayloadExpired
	}
	payload := json.RawMessage(plaintext)
	view, err := accountJobPayloadView(payload)
	if err != nil {
		return nil, nil, err
	}
	stored, err := AccountJobViewExecution(job.Metadata)
	if err != nil {
		return nil, nil, err
	}
	if (stored == nil) != (view == nil) || (stored != nil && (stored.AccountViewIdentityV1 != view.AccountViewIdentityV1 || stored.NormalizedQueryDigest != accountViewQueryDigest(view.Query))) {
		return nil, nil, ErrAccountViewUnavailable
	}
	ids, err := accountViewJobTargets(job.Kind, payload, nil)
	return view, ids, err
}

func prepareAccountJobSubmission(ctx context.Context, metadata json.RawMessage, kind string) (context.Context, json.RawMessage, func(), error) {
	if err := validateAccountViewJobKind(ctx, kind, metadata); err != nil {
		return nil, nil, nil, err
	}
	owner, err := AccountJobPluginExecution(metadata)
	if err != nil {
		return nil, nil, nil, err
	}
	if execution, bound := PluginExecutionFromContext(ctx); bound {
		raw, err := stampAccountJobPlugin(metadata, execution)
		return ctx, raw, func() {}, err
	}
	if owner.ID == 0 {
		return ctx, metadata, func() {}, nil
	}
	bound, release, err := bindAccountJobPlugin(ctx, owner, kind)
	if err != nil {
		return nil, nil, nil, err
	}
	execution, _ := PluginExecutionFromContext(bound)
	raw, err := stampAccountJobPlugin(metadata, execution)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	return bound, raw, release, nil
}
