//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The normal integration harness applies the actual migrations to PostgreSQL.
// Only the pure policy builder is a fixture: Preview/Apply, evidence snapshots,
// JSONB patches, channels, locks and transactional outboxes are the real paths.
// No discovery/probe worker or upstream model request is started by this test.
func TestAccountCapabilityPublicationIntegration(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	prefix := fmt.Sprintf("capability-publication-%d", time.Now().UnixNano())
	var accountIDs, groupIDs, changeSetIDs, runIDs []int64
	var folderID, actorID, proxyID, keyID, runID, sharedChannelID int64
	var cacheKey string
	t.Cleanup(func() {
		channelNames := make([]string, 0, len(groupIDs))
		for _, id := range groupIDs {
			channelNames = append(channelNames, fmt.Sprintf("public-capabilities-g%d", id))
		}
		// Every cleanup target is a row created by this fixture; the shared
		// integration database and unrelated durable evidence are not truncated.
		for _, stmt := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM admin_capability_changesets WHERE id=ANY($1)`, []any{pq.Array(changeSetIDs)}},
			{`DELETE FROM admin_capability_items WHERE run_id=ANY($1)`, []any{pq.Array(runIDs)}},
			{`DELETE FROM admin_capability_runs WHERE id=ANY($1)`, []any{pq.Array(runIDs)}},
			{`DELETE FROM api_keys WHERE id=$1`, []any{keyID}},
			{`DELETE FROM accounts WHERE id=ANY($1)`, []any{pq.Array(accountIDs)}},
			{`DELETE FROM groups WHERE id=ANY($1)`, []any{pq.Array(groupIDs)}},
			{`DELETE FROM channels WHERE id=$1 OR name=ANY($2)`, []any{sharedChannelID, pq.Array(channelNames)}},
			{`DELETE FROM users WHERE id=$1`, []any{actorID}},
			{`DELETE FROM proxies WHERE id=$1`, []any{proxyID}},
			{`DELETE FROM account_folders WHERE id=$1`, []any{folderID}},
			{`DELETE FROM scheduler_outbox WHERE account_id=ANY($1) OR group_id=ANY($2)`, []any{pq.Array(accountIDs), pq.Array(groupIDs)}},
			{`DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=$1`, []any{cacheKey}},
		} {
			_, err := integrationDB.ExecContext(ctx, stmt.query, stmt.args...)
			require.NoError(t, err, "clean publication fixture")
		}
	})
	queryJSON := func(query string, args ...any) string {
		t.Helper()
		var raw string
		require.NoError(t, integrationDB.QueryRowContext(ctx, query, args...).Scan(&raw))
		return raw
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	folder, err := client.AccountFolder.Create().SetName(prefix).SetNormalizedName(prefix).Save(ctx)
	require.NoError(t, err)
	folderID = folder.ID
	actor := mustCreateUser(t, client, &service.User{Email: prefix + "@example.invalid", Balance: 123.45})
	actorID = actor.ID
	proxy := mustCreateProxy(t, client, &service.Proxy{Name: prefix, Host: "127.0.0.1", Port: 18081})
	proxyID = proxy.ID
	public := mustCreateGroup(t, client, &service.Group{Name: prefix + "-public", Platform: service.PlatformOpenAI, RateMultiplier: 0.2})
	groupIDs = append(groupIDs, public.ID)
	private := mustCreateGroup(t, client, &service.Group{Name: prefix + "-private", Platform: service.PlatformOpenAI, RateMultiplier: 0.7, IsExclusive: true})
	groupIDs = append(groupIDs, private.ID)
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	live := mustCreateAccount(t, client, &service.Account{
		Name: prefix + "-live", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Priority: 31, Concurrency: 7, ProxyID: &proxyID, RateLimitResetAt: &later, OverloadUntil: &later,
		Credentials: map[string]any{
			"api_key": "fixture-only-never-send", "base_url": "https://example.invalid",
			"model_mapping": map[string]any{"private-alias": "Private/Model", "s2pub-retired": "Retired/Model"},
		},
		Extra: map[string]any{
			"openai_responses_mode":               "force_responses",
			service.ModelContextOverridesExtraKey: map[string]any{"Vendor/Model": 258000},
			"unrelated_setting":                   true,
		},
	})
	accountIDs = append(accountIDs, live.ID)
	_, err = client.Account.UpdateOneID(live.ID).SetSchedulable(false).SetManagementFolderID(folderID).Save(ctx)
	require.NoError(t, err)
	live.Schedulable, live.ManagementFolderID = false, &folderID
	live.Proxy = proxy
	dead := mustCreateAccount(t, client, &service.Account{Name: prefix + "-dead", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-terminal", "model_mapping": map[string]any{"private-alias": "Private/Other"}}})
	accountIDs = append(accountIDs, dead.ID)
	_, err = client.Account.UpdateOneID(dead.ID).SetManagementFolderID(folderID).Save(ctx)
	require.NoError(t, err)
	dead.ManagementFolderID = &folderID
	legacy := mustCreateAccount(t, client, &service.Account{Name: prefix + "-out-of-scope", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{"preserve": "entire account"}})
	accountIDs = append(accountIDs, legacy.ID)
	mustBindAccountToGroup(t, client, live.ID, private.ID, 7)
	mustBindAccountToGroup(t, client, dead.ID, private.ID, 9)
	mustBindAccountToGroup(t, client, dead.ID, public.ID, 13)
	mustBindAccountToGroup(t, client, legacy.ID, private.ID, 11)
	mustBindAccountToGroup(t, client, legacy.ID, public.ID, 17)
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: actorID, GroupID: &public.ID, Key: "sk-" + prefix, Quota: 42, QuotaUsed: 3})
	keyID = key.ID
	hash := sha256.Sum256([]byte(key.Key))
	cacheKey = hex.EncodeToString(hash[:])
	price := 0.000002
	shared := &service.Channel{
		Name: prefix + "-shared", Status: service.StatusActive, BillingModelSource: service.BillingModelSourceUpstream,
		GroupIDs: []int64{public.ID, private.ID}, Features: "fixture feature", FeaturesConfig: map[string]any{"enabled": true},
		ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {"private-alias": "Private/Model"}},
		ModelPricing: []service.ChannelModelPricing{{Platform: service.PlatformOpenAI, Models: []string{"fixture-model"}, BillingMode: service.BillingModeToken, InputPrice: &price, OutputPrice: &price}},
	}
	require.NoError(t, NewChannelRepository(integrationDB).Create(ctx, shared))
	sharedChannelID = shared.ID
	jobs := NewAccountCapabilityRepository(integrationDB)
	seed := &service.AccountCapabilityRun{CreatedBy: actorID, Kind: "probe", IdempotencyKey: prefix, RequestHash: strings.Repeat("a", 64), FolderIDs: []int64{folderID}, AccountIDs: []int64{live.ID, dead.ID}}
	items := []service.AccountCapabilityItem{
		{Ordinal: 1, AccountID: live.ID, AccountName: live.Name, FolderID: folderID, ConfigFingerprint: service.ManagedModelAccountFingerprint(live), UpstreamModel: "Vendor/Model", Protocol: "responses", Profile: "text"},
		{Ordinal: 2, AccountID: live.ID, AccountName: live.Name, FolderID: folderID, ConfigFingerprint: service.ManagedModelAccountFingerprint(live), UpstreamModel: "Vendor/Model-SSVIP", Protocol: "responses", Profile: "text"},
		{Ordinal: 3, AccountID: dead.ID, AccountName: dead.Name, FolderID: folderID, ConfigFingerprint: service.ManagedModelAccountFingerprint(dead), UpstreamModel: "Vendor/Model", Protocol: "responses", Profile: "text"},
	}
	run, _, err := jobs.Create(ctx, seed, items)
	require.NoError(t, err)
	runID = run.ID
	runIDs = append(runIDs, runID)
	page, err := jobs.ListItems(ctx, runID, service.AccountCapabilityFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	evidenceIDs := make([]int64, 3)
	for _, item := range page.Items {
		status := "succeeded"
		result := service.AccountCapabilityProbeResult{Status: "alive", Classification: "completed", RequestCount: 1, UpstreamModel: item.UpstreamModel, Protocol: item.Protocol, Profile: item.Profile}
		if item.AccountID == dead.ID {
			status = "failed"
			result.Status, result.Classification, result.AccountFailure = "failed", "credential_invalid", true
		}
		raw, marshalErr := json.Marshal(result)
		require.NoError(t, marshalErr)
		_, err = integrationDB.ExecContext(ctx, `UPDATE admin_capability_items SET status=$2,result=$3::jsonb,request_count=1,finished_at=NOW() WHERE id=$1`, item.ID, status, string(raw))
		require.NoError(t, err)
		evidenceIDs[item.Ordinal-1] = item.ID
	}
	_, err = integrationDB.ExecContext(ctx, `UPDATE admin_capability_runs SET status='completed',finished_at=NOW() WHERE id=$1`, runID)
	require.NoError(t, err)

	request := service.CapabilityPublicationRequest{
		IdempotencyKey: prefix + "-apply", Scope: service.CapabilityPublicationScope{FolderIDs: []int64{folderID}, AccountIDs: []int64{live.ID, dead.ID}},
		DetachAccountIDs: []int64{legacy.ID}, SchedulingEvidenceIDs: []int64{evidenceIDs[2]},
		Groups: []service.CapabilityPublicationGroup{
			{ID: public.ID, Name: public.Name, Platform: service.PlatformOpenAI, RateMultiplier: 0.2, Models: []service.CapabilityPublicationModel{{PublicModel: "fixture-model", EvidenceIDs: []int64{evidenceIDs[0]}}}},
			// Unique fixture naming isolates this repository-level reserved-ID test;
			// the service's Qwen/MiniMax name policy is covered in its unit tests.
			{Name: prefix + "-new", Platform: service.PlatformOpenAI, RateMultiplier: 0.3, Models: []service.CapabilityPublicationModel{{PublicModel: "fixture-model", Tier: "vip", EvidenceIDs: []int64{evidenceIDs[1]}}}},
		},
	}
	build := func(snap *service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
		require.Len(t, snap.Accounts, 3)
		require.Len(t, snap.Evidence, 3)
		require.Equal(t, 7, snap.Accounts[live.ID].Bindings[private.ID])
		require.Equal(t, folderID, *snap.Accounts[live.ID].Account.ManagementFolderID)
		enable, disable := true, false
		plan := &service.CapabilityPublicationPlan{Accounts: []service.CapabilityPublicationAccountPatch{
			{AccountID: live.ID, ModelMapping: map[string]string{}, RemoveSelectors: []string{"s2pub-retired"}, Schedulable: &enable},
			{AccountID: dead.ID, RemoveGroupIDs: []int64{public.ID}, Schedulable: &disable},
			{AccountID: legacy.ID, RemoveGroupIDs: []int64{public.ID}},
		}}
		for _, input := range snap.Request.Groups {
			evidence := snap.Evidence[input.Models[0].EvidenceIDs[0]]
			require.Equal(t, "succeeded", evidence.Status)
			require.Len(t, evidence.ConfigFingerprint, 64)
			require.Equal(t, evidence.ConfigFingerprint, service.ManagedModelAccountFingerprint(snap.Accounts[evidence.AccountID].Account), "snapshot must hydrate the effective proxy used by the probe")
			require.NotNil(t, evidence.FinishedAt)
			require.False(t, evidence.Superseded)
			require.Equal(t, seed.AccountIDs, evidence.RunAccountIDs)
			selector := service.CapabilityPublicationSelector(input.ID, "fixture-model")
			plan.Accounts[0].ModelMapping[selector] = evidence.UpstreamModel
			plan.Accounts[0].AddGroupIDs = append(plan.Accounts[0].AddGroupIDs, input.ID)
			group := service.CapabilityPublicationGroupPatch{
				GroupID: input.ID, Create: snap.Groups[input.ID].IsNew, Name: input.Name, Platform: input.Platform, RateMultiplier: input.RateMultiplier, Status: service.StatusActive,
				ManagedModelRoutes: domain.ManagedModelRoutesConfig{Version: 1, Enabled: true, Routes: []domain.ManagedModelRoute{{PublicModel: "fixture-model", Selector: selector, TargetPlatform: service.PlatformOpenAI, Endpoints: []string{"responses", "messages"}, Accounts: []domain.ManagedModelRouteAccount{{AccountID: live.ID, UpstreamModel: evidence.UpstreamModel, AccountFingerprint: evidence.ConfigFingerprint, Endpoints: []string{"responses", "messages"}}}}}},
				ModelAllowlist:     service.GroupModelAllowlist{Enabled: true, Models: []string{"fixture-model"}},
				MessagesDispatch:   service.OpenAIMessagesDispatchModelConfig{ExactModelMappings: map[string]string{"fixture-model": selector}},
				ChannelMapping:     map[string]map[string]string{service.PlatformOpenAI: {"fixture-model": selector}},
				ChannelPricing:     shared.ModelPricing, ChannelFeatures: shared.Features, ChannelFeaturesConfig: shared.FeaturesConfig,
			}
			if oldChannel := snap.Groups[input.ID].Channel; oldChannel != nil {
				require.Equal(t, shared.ID, oldChannel.ID)
				require.Len(t, oldChannel.ModelPricing, 1)
				group.ChannelPricing = oldChannel.ModelPricing
			}
			plan.Groups = append(plan.Groups, group)
			plan.Changes = append(plan.Changes, service.CapabilityPublicationChange{Kind: "routes", GroupID: input.ID, After: group.ManagedModelRoutes, EvidenceIDs: input.Models[0].EvidenceIDs})
		}
		return plan, nil
	}
	repo := NewAccountCapabilityPublicationRepository(integrationDB)
	// Full JSON snapshots include timestamps and row IDs, so an idempotent
	// replay cannot hide a redundant UPDATE, channel replacement or outbox insert.
	state := func() string {
		return queryJSON(`SELECT jsonb_build_object(
		 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a WHERE id=ANY($1)),
		 'groups',(SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM groups g WHERE id=ANY($2)),
		 'bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY account_id,group_id) FROM account_groups b WHERE account_id=ANY($1)),
		 'channels',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM channels c WHERE id=$3 OR id IN(SELECT channel_id FROM channel_groups WHERE group_id=ANY($2))),
		 'channel_bindings',(SELECT jsonb_agg(to_jsonb(cg) ORDER BY id) FROM channel_groups cg WHERE group_id=ANY($2)),
		 'pricing',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_model_pricing p WHERE channel_id=$3 OR channel_id IN(SELECT channel_id FROM channel_groups WHERE group_id=ANY($2))),
		 'key',(SELECT to_jsonb(k) FROM api_keys k WHERE id=$4),
		 'scheduler',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM scheduler_outbox o WHERE account_id=ANY($1) OR group_id=ANY($2)),
		 'auth',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM auth_cache_invalidation_outbox o WHERE cache_key=$5)
		)::text`, pq.Array(accountIDs), pq.Array(groupIDs), sharedChannelID, keyID, cacheKey)
	}
	protectedAccounts := func() string {
		return queryJSON(`SELECT jsonb_agg((to_jsonb(a)-ARRAY['updated_at','credentials','schedulable']) || jsonb_build_object('credentials',credentials-'model_mapping','private_mapping',credentials->'model_mapping'->'private-alias') ORDER BY id)::text FROM accounts a WHERE id=ANY($1)`, pq.Array(accountIDs))
	}
	protectedBefore := protectedAccounts()
	legacyBefore := queryJSON(`SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, legacy.ID)
	privateBefore := queryJSON(`SELECT to_jsonb(g)::text FROM groups g WHERE id=$1`, private.ID)
	keyBefore := queryJSON(`SELECT to_jsonb(k)::text FROM api_keys k WHERE id=$1`, keyID)
	channelBefore := queryJSON(`SELECT to_jsonb(c)::text FROM channels c WHERE id=$1`, sharedChannelID)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=$1`, cacheKey)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=ANY($1) OR group_id=ANY($2)`, pq.Array(accountIDs), pq.Array(groupIDs))
	require.NoError(t, err)
	beforePreview := state()
	preview, err := repo.Preview(ctx, request, build)
	require.NoError(t, err)
	changeSetIDs = append(changeSetIDs, preview.ID)
	require.Equal(t, "preview", preview.Status)
	require.JSONEq(t, beforePreview, state(), "preview is observational for all business state")
	newGroupID := preview.Request.Groups[1].ID
	require.Positive(t, newGroupID)
	groupIDs = append(groupIDs, newGroupID)
	require.Zero(t, count(`SELECT COUNT(*) FROM groups WHERE id=$1`, newGroupID), "preview reserves an ID without creating the public group")
	again, err := repo.Preview(ctx, request, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
		t.Fatal("idempotent preview must not rebuild")
		return nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, preview.ID, again.ID)
	require.Equal(t, newGroupID, again.Request.Groups[1].ID)
	applied, err := repo.Apply(ctx, preview.ID, build)
	require.NoError(t, err)
	require.Equal(t, "applied", applied.Status)
	require.NotNil(t, applied.AppliedAt)
	require.JSONEq(t, protectedBefore, protectedAccounts(), "private credentials, capacity, priority, proxy and cooldown fields survive")
	require.JSONEq(t, legacyBefore, queryJSON(`SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, legacy.ID), "out-of-scope account scheduling and its entire row are untouched")
	require.JSONEq(t, privateBefore, queryJSON(`SELECT to_jsonb(g)::text FROM groups g WHERE id=$1`, private.ID))
	require.JSONEq(t, keyBefore, queryJSON(`SELECT to_jsonb(k)::text FROM api_keys k WHERE id=$1`, keyID))
	require.JSONEq(t, channelBefore, queryJSON(`SELECT to_jsonb(c)::text FROM channels c WHERE id=$1`, sharedChannelID), "the old shared channel is not rewritten")
	require.Equal(t, 7, count(`SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, live.ID, private.ID))
	require.Equal(t, 9, count(`SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, dead.ID, private.ID))
	require.Equal(t, 11, count(`SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, legacy.ID, private.ID))
	require.Equal(t, 2, count(`SELECT COUNT(*) FROM account_groups WHERE account_id=$1 AND group_id=ANY($2)`, live.ID, pq.Array([]int64{public.ID, newGroupID})))
	require.Zero(t, count(`SELECT COUNT(*) FROM account_groups WHERE account_id=ANY($1) AND group_id=$2`, pq.Array([]int64{dead.ID, legacy.ID}), public.ID))
	require.Equal(t, 1, count(`SELECT COUNT(*) FROM accounts WHERE id=$1 AND schedulable`, live.ID))
	require.Equal(t, 1, count(`SELECT COUNT(*) FROM accounts WHERE id=$1 AND NOT schedulable`, dead.ID))
	mapping := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(queryJSON(`SELECT (credentials->'model_mapping')::text FROM accounts WHERE id=$1`, live.ID)), &mapping))
	require.Equal(t, map[string]string{"private-alias": "Private/Model", service.CapabilityPublicationSelector(public.ID, "fixture-model"): "Vendor/Model", service.CapabilityPublicationSelector(newGroupID, "fixture-model"): "Vendor/Model-SSVIP"}, mapping)
	for _, gp := range applied.Plan.Groups {
		raw, marshalErr := json.Marshal(gp.ManagedModelRoutes)
		require.NoError(t, marshalErr)
		require.JSONEq(t, string(raw), queryJSON(`SELECT managed_model_routes::text FROM groups WHERE id=$1`, gp.GroupID))
		require.JSONEq(t, `{"enabled":true,"models":["fixture-model"]}`, queryJSON(`SELECT model_allowlist::text FROM groups WHERE id=$1`, gp.GroupID))
		var actualRate float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT rate_multiplier FROM groups WHERE id=$1`, gp.GroupID).Scan(&actualRate))
		require.Equal(t, gp.RateMultiplier, actualRate)
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM channels c JOIN channel_groups cg ON cg.channel_id=c.id WHERE cg.group_id=$1 AND c.billing_model_source='requested' AND c.name=$2`, gp.GroupID, fmt.Sprintf("public-capabilities-g%d", gp.GroupID)))
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM channel_model_pricing p JOIN channel_groups cg ON cg.channel_id=p.channel_id WHERE cg.group_id=$1 AND p.input_price=$2`, gp.GroupID, price))
		require.Positive(t, count(`SELECT COUNT(*) FROM scheduler_outbox WHERE group_id=$1`, gp.GroupID), "group invalidation is committed durably")
	}
	require.Equal(t, 1, count(`SELECT COUNT(*) FROM channel_groups WHERE group_id=$1 AND channel_id=$2`, private.ID, sharedChannelID))
	require.Positive(t, count(`SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key=$1`, cacheKey), "bound key auth invalidation is committed durably")
	for _, id := range accountIDs {
		require.Positive(t, count(`SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2`, id, service.SchedulerOutboxEventAccountChanged))
	}
	audit := queryJSON(`SELECT jsonb_build_object('request',request,'plan',plan)::text FROM admin_capability_changesets WHERE id=$1`, preview.ID)
	require.NotContains(t, audit, "fixture-only-never-send")
	require.NotContains(t, audit, "fixture-terminal")
	afterApply := state()
	replayed, err := repo.Apply(ctx, preview.ID, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
		t.Fatal("an applied changeset must not rebuild or replay business mutations")
		return nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, applied.AppliedAt, replayed.AppliedAt)
	require.JSONEq(t, afterApply, state())

	t.Run("stale_private_binding_aborts_entire_changeset", func(t *testing.T) {
		conflictRequest := service.CapabilityPublicationRequest{IdempotencyKey: prefix + "-conflict", Scope: request.Scope, Groups: []service.CapabilityPublicationGroup{{ID: public.ID, Name: public.Name, Platform: service.PlatformOpenAI, RateMultiplier: 0.2}}}
		disable := false
		conflictBuilder := func(snap *service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
			return &service.CapabilityPublicationPlan{
				Groups:   []service.CapabilityPublicationGroupPatch{{GroupID: public.ID, Name: public.Name, Platform: service.PlatformOpenAI, RateMultiplier: 0.2, Status: "inactive", ManagedModelRoutes: domain.ManagedModelRoutesConfig{Version: 2, Enabled: true}, ModelAllowlist: service.GroupModelAllowlist{Enabled: true}}},
				Accounts: []service.CapabilityPublicationAccountPatch{{AccountID: live.ID, Schedulable: &disable, RemoveSelectors: []string{service.CapabilityPublicationSelector(public.ID, "fixture-model")}, RemoveGroupIDs: []int64{public.ID}}},
			}, nil
		}
		stale, previewErr := repo.Preview(ctx, conflictRequest, conflictBuilder)
		require.NoError(t, previewErr)
		changeSetIDs = append(changeSetIDs, stale.ID)
		// A concurrent private binding edit is outside this publication's patch,
		// but is a real dependency and must invalidate the complete preview.
		_, updateErr := integrationDB.ExecContext(ctx, `UPDATE account_groups SET priority=19 WHERE account_id=$1 AND group_id=$2`, live.ID, private.ID)
		require.NoError(t, updateErr)
		beforeConflict := state()
		_, applyErr := repo.Apply(ctx, stale.ID, func(*service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
			t.Fatal("snapshot CAS conflict must stop before rebuilding or writing any group/account")
			return nil, nil
		})
		require.ErrorIs(t, applyErr, service.ErrCapabilityPublicationConflict)
		require.JSONEq(t, beforeConflict, state(), "a stale publication leaves no partial group, mapping, scheduling or outbox mutation")
		stored, getErr := repo.Get(ctx, stale.ID)
		require.NoError(t, getErr)
		require.Equal(t, "preview", stored.Status)
		require.Nil(t, stored.AppliedAt)
	})

	t.Run("later_negative_evidence_rejects_old_alive", func(t *testing.T) {
		for _, tc := range []struct {
			name, status, classification string
			evidenceIndex                int
			sameFinishedAt               bool
		}{
			{name: "later_model_failure", status: "failed", classification: "model_unavailable", evidenceIndex: 0},
			{name: "same_timestamp_timeout_with_larger_id", status: "indeterminate", classification: "timeout", evidenceIndex: 1, sameFinishedAt: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				oldID := evidenceIDs[tc.evidenceIndex]
				item := items[tc.evidenceIndex]
				item.Ordinal = 1
				// A repeat attempt belongs to a new run: the schema deliberately
				// deduplicates this account/model/protocol/profile within each run.
				retestSeed := &service.AccountCapabilityRun{CreatedBy: actorID, Kind: "probe", IdempotencyKey: prefix + "-" + tc.name, RequestHash: strings.Repeat("b", 64), FolderIDs: []int64{folderID}, AccountIDs: []int64{live.ID}}
				retest, _, createErr := jobs.Create(ctx, retestSeed, []service.AccountCapabilityItem{item})
				require.NoError(t, createErr)
				runIDs = append(runIDs, retest.ID)
				var oldFinishedAt time.Time
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT finished_at FROM admin_capability_items WHERE id=$1`, oldID).Scan(&oldFinishedAt))
				finishedAt := oldFinishedAt.Add(time.Second)
				if tc.sameFinishedAt {
					finishedAt = oldFinishedAt
				}
				result, marshalErr := json.Marshal(service.AccountCapabilityProbeResult{Status: tc.status, Classification: tc.classification, RequestCount: 1, UpstreamModel: item.UpstreamModel, Protocol: item.Protocol, Profile: item.Profile})
				require.NoError(t, marshalErr)
				var newerID int64
				require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE admin_capability_items SET status=$2,result=$3::jsonb,request_count=1,finished_at=$4 WHERE run_id=$1 RETURNING id`, retest.ID, tc.status, string(result), finishedAt).Scan(&newerID))
				require.Greater(t, newerID, oldID, "equal timestamps are ordered by the durable item ID")
				_, updateErr := integrationDB.ExecContext(ctx, `UPDATE admin_capability_runs SET status='completed',finished_at=$2 WHERE id=$1`, retest.ID, finishedAt)
				require.NoError(t, updateErr)
				staleRequest := service.CapabilityPublicationRequest{
					IdempotencyKey: prefix + "-stale-" + tc.name, Scope: request.Scope,
					Groups: []service.CapabilityPublicationGroup{{ID: public.ID, Name: public.Name, Platform: service.PlatformOpenAI, RateMultiplier: 0.2, Models: []service.CapabilityPublicationModel{{PublicModel: "fixture-model", EvidenceIDs: []int64{oldID}}}}},
				}
				beforeRejection := state()
				seenSnapshot := false
				rejected, previewErr := repo.Preview(ctx, staleRequest, func(snap *service.CapabilityPublicationSnapshot) (*service.CapabilityPublicationPlan, error) {
					seenSnapshot = true
					old := snap.Evidence[oldID]
					require.Equal(t, "succeeded", old.Status, "old evidence stays immutable")
					require.True(t, old.Superseded, "a later failure of this exact route revokes its old alive evidence")
					if old.Superseded {
						return nil, service.ErrCapabilityPublicationInvalid
					}
					return &service.CapabilityPublicationPlan{}, nil
				})
				require.True(t, seenSnapshot)
				require.ErrorIs(t, previewErr, service.ErrCapabilityPublicationInvalid)
				require.Nil(t, rejected)
				require.Zero(t, count(`SELECT COUNT(*) FROM admin_capability_changesets WHERE idempotency_key=$1`, staleRequest.IdempotencyKey))
				require.JSONEq(t, beforeRejection, state(), "rejected old evidence must not change public routes, accounts or outboxes")
			})
		}
	})

	t.Run("catalog_recovery_retires_terminal_failure_without_reviving_old_model", func(t *testing.T) {
		beforeSequence := state()
		var baseTime time.Time
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(finished_at),NOW()) FROM admin_capability_items WHERE account_id=$1`, live.ID).Scan(&baseTime))
		seedObservation := func(label, kind, model, status string, result any, offset time.Duration) int64 {
			t.Helper()
			item := service.AccountCapabilityItem{Ordinal: 1, AccountID: live.ID, AccountName: live.Name, FolderID: folderID, ConfigFingerprint: items[0].ConfigFingerprint, UpstreamModel: model, Profile: "text"}
			if kind == service.AccountCapabilityKindProbe {
				item.Protocol = "responses"
			}
			observationRun, _, createErr := jobs.Create(ctx, &service.AccountCapabilityRun{CreatedBy: actorID, Kind: kind, IdempotencyKey: prefix + "-catalog-recovery-" + label, RequestHash: strings.Repeat("c", 64), FolderIDs: []int64{folderID}, AccountIDs: []int64{live.ID}}, []service.AccountCapabilityItem{item})
			require.NoError(t, createErr)
			runIDs = append(runIDs, observationRun.ID)
			raw, marshalErr := json.Marshal(result)
			require.NoError(t, marshalErr)
			finishedAt := baseTime.Add(offset)
			var id int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE admin_capability_items SET status=$2,result=$3::jsonb,request_count=1,finished_at=$4 WHERE run_id=$1 RETURNING id`, observationRun.ID, status, string(raw), finishedAt).Scan(&id))
			_, updateErr := integrationDB.ExecContext(ctx, `UPDATE admin_capability_runs SET status='completed',finished_at=$2 WHERE id=$1`, observationRun.ID, finishedAt)
			require.NoError(t, updateErr)
			return id
		}
		assertVerdicts := func(ids, projectedIDs []int64, expected map[int64]bool) {
			t.Helper()
			observed, getErr := jobs.GetItemsByIDs(ctx, ids)
			require.NoError(t, getErr)
			require.Len(t, observed, len(expected))
			latest, latestErr := jobs.LatestItems(ctx, service.AccountCapabilityFilter{AccountID: live.ID, PageSize: 100})
			require.NoError(t, latestErr)
			projected := map[int64]bool{}
			for _, item := range latest.Items {
				if _, relevant := expected[item.ID]; relevant {
					projected[item.ID] = item.PublicationSuperseded
				}
			}
			require.Len(t, projected, len(projectedIDs))
			for _, id := range projectedIDs {
				flag, exists := projected[id]
				require.True(t, exists, "latest projection should retain item %d", id)
				require.Equal(t, expected[id], flag)
			}
			// Read the same IDs through publication's real locked snapshot. No
			// groups are requested, so this neither reserves IDs nor writes a plan.
			tx, beginErr := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, beginErr)
			defer func() { _ = tx.Rollback() }()
			publicationRepo := &accountCapabilityPublicationRepository{db: integrationDB}
			snap, snapshotErr := publicationRepo.snapshot(ctx, tx, service.CapabilityPublicationRequest{Scope: service.CapabilityPublicationScope{FolderIDs: []int64{folderID}, AccountIDs: []int64{live.ID}}, SchedulingEvidenceIDs: ids}, nil)
			require.NoError(t, snapshotErr)
			require.Len(t, snap.Evidence, len(expected))
			for _, item := range observed {
				require.Equal(t, expected[item.ID], item.PublicationSuperseded, "ledger verdict for item %d", item.ID)
				require.Equal(t, item.PublicationSuperseded, snap.Evidence[item.ID].Superseded, "inventory and publication must use the same full-ledger verdict for item %d", item.ID)
			}
		}
		model := "Fixture/Catalog-Recovery-A"
		aliveID := seedObservation("alive", service.AccountCapabilityKindProbe, model, "succeeded", service.AccountCapabilityProbeResult{Status: "alive", Classification: "completed", UpstreamModel: model, Protocol: "responses", Profile: "text", RequestCount: 1}, time.Second)
		assertVerdicts([]int64{aliveID}, []int64{aliveID}, map[int64]bool{aliveID: false})
		terminalID := seedObservation("terminal", service.AccountCapabilityKindDiscover, "", "failed", service.AccountCapabilityDiscoveryResult{Status: "failed", Classification: "credential_invalid", Source: "upstream", AccountFailure: true, RequestCount: 1}, 2*time.Second)
		assertVerdicts([]int64{aliveID, terminalID}, []int64{aliveID, terminalID}, map[int64]bool{aliveID: true, terminalID: false})
		catalogID := seedObservation("catalog", service.AccountCapabilityKindDiscover, "", "succeeded", service.AccountCapabilityDiscoveryResult{Status: "discovered", Source: "upstream", HTTPStatus: 200, RequestCount: 1, Models: []service.AccountCapabilityDiscoveredModel{{ID: model, DisplayName: model}}}, 3*time.Second)
		// LatestItems now drops the older discovery failure. It must still look
		// behind that projection: catalog recovery cannot revive the old model,
		// and the superseded credential failure must not disable the account.
		assertVerdicts([]int64{aliveID, terminalID, catalogID}, []int64{aliveID, catalogID}, map[int64]bool{aliveID: true, terminalID: true, catalogID: false})
		require.JSONEq(t, beforeSequence, state(), "reading discovery recovery cannot mutate scheduling, routes or outboxes")
	})
}
