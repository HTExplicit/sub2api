package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountCapabilityPublicationRepository struct{ db *sql.DB }

func NewAccountCapabilityPublicationRepository(db *sql.DB) service.AccountCapabilityPublicationRepository {
	return &accountCapabilityPublicationRepository{db: db}
}

func (r *accountCapabilityPublicationRepository) Preview(ctx context.Context, req service.CapabilityPublicationRequest, build service.CapabilityPublicationBuilder) (*service.CapabilityChangeSet, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = publicationTxLimits(ctx, tx); err != nil {
		return nil, err
	}
	// Serialize the idempotency key before consuming any group sequence value.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 481627))`, req.IdempotencyKey); err != nil {
		return nil, publicationDBError(err)
	}
	set, err := publicationReadChangeSet(ctx, tx, `idempotency_key = $1`, req.IdempotencyKey, false)
	if err == nil {
		if !publicationRequestsEqual(req, set.Request) {
			return nil, service.ErrCapabilityPublicationConflict
		}
		return set, nil
	}
	if !errors.Is(err, service.ErrCapabilityChangeSetNotFound) {
		return nil, err
	}
	snap, err := r.snapshot(ctx, tx, req, nil)
	if err != nil {
		return nil, publicationDBError(err)
	}
	plan, err := build(snap)
	if err != nil {
		return nil, err
	}
	requestJSON, err := json.Marshal(snap.Request)
	if err != nil {
		return nil, err
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	set = &service.CapabilityChangeSet{Status: "preview", Request: snap.Request, Plan: *plan, BeforeFingerprint: snap.Fingerprint, Changes: plan.Changes, Warnings: plan.Warnings}
	set.Scope = snap.Request.Scope
	err = tx.QueryRowContext(ctx, `INSERT INTO admin_capability_changesets(idempotency_key,request,plan,before_fingerprint,status) VALUES($1,$2::jsonb,$3::jsonb,$4,'preview') RETURNING id,created_at`, req.IdempotencyKey, string(requestJSON), string(planJSON), snap.Fingerprint).Scan(&set.ID, &set.CreatedAt)
	if err != nil {
		return nil, publicationDBError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, publicationDBError(err)
	}
	return set, nil
}

func (r *accountCapabilityPublicationRepository) Get(ctx context.Context, id int64) (*service.CapabilityChangeSet, error) {
	return publicationReadChangeSet(ctx, r.db, `id = $1`, id, false)
}

func (r *accountCapabilityPublicationRepository) Apply(ctx context.Context, id int64, build service.CapabilityPublicationBuilder) (*service.CapabilityChangeSet, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = publicationTxLimits(ctx, tx); err != nil {
		return nil, err
	}
	set, err := publicationReadChangeSet(ctx, tx, `id = $1`, id, true)
	if err != nil {
		return nil, err
	}
	if set.Status == "applied" {
		return set, nil
	}
	if set.Status != "preview" {
		return nil, service.ErrCapabilityPublicationConflict
	}
	reserved := map[int64]bool{}
	for _, group := range set.Plan.Groups {
		if group.Create {
			reserved[group.GroupID] = true
		}
	}
	snap, err := r.snapshot(ctx, tx, set.Request, reserved)
	if err != nil {
		return nil, publicationDBError(err)
	}
	if snap.Fingerprint != set.BeforeFingerprint {
		return nil, service.ErrCapabilityPublicationConflict
	}
	current, err := build(snap)
	if err != nil {
		return nil, err
	}
	if !service.CapabilityPublicationPlansEqual(&set.Plan, current) {
		return nil, service.ErrCapabilityPublicationConflict
	}
	for _, gp := range set.Plan.Groups {
		if err = publicationApplyGroup(ctx, tx, gp); err != nil {
			return nil, publicationDBError(err)
		}
	}
	for _, ap := range set.Plan.Accounts {
		if err = publicationApplyAccount(ctx, tx, ap); err != nil {
			return nil, publicationDBError(err)
		}
	}
	for _, gp := range set.Plan.Groups {
		// Existing group triggers do not cover all newly managed JSON fields.
		if _, err = tx.ExecContext(ctx, `SELECT enqueue_channel_group_cache_invalidations($1)`, gp.GroupID); err != nil {
			return nil, publicationDBError(err)
		}
	}
	err = tx.QueryRowContext(ctx, `UPDATE admin_capability_changesets SET status='applied',applied_at=NOW() WHERE id=$1 AND status='preview' RETURNING applied_at`, id).Scan(&set.AppliedAt)
	if err != nil {
		return nil, publicationDBError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, publicationDBError(err)
	}
	set.Status = "applied"
	return set, nil
}

type publicationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func publicationReadChangeSet(ctx context.Context, q publicationQuerier, predicate string, arg any, lock bool) (*service.CapabilityChangeSet, error) {
	query := `SELECT id,status,request,plan,before_fingerprint,created_at,applied_at FROM admin_capability_changesets WHERE ` + predicate
	if lock {
		query += ` FOR UPDATE`
	}
	set := &service.CapabilityChangeSet{}
	var requestJSON, planJSON []byte
	err := q.QueryRowContext(ctx, query, arg).Scan(&set.ID, &set.Status, &requestJSON, &planJSON, &set.BeforeFingerprint, &set.CreatedAt, &set.AppliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCapabilityChangeSetNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(requestJSON, &set.Request); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(planJSON, &set.Plan); err != nil {
		return nil, err
	}
	set.Changes, set.Warnings = set.Plan.Changes, set.Plan.Warnings
	set.Scope = set.Request.Scope
	return set, nil
}

func publicationRequestsEqual(in, stored service.CapabilityPublicationRequest) bool {
	// A saved preview contains reserved IDs for new groups; the caller still
	// submits ID=0 on an idempotent retry.
	if len(in.Groups) != len(stored.Groups) {
		return false
	}
	stored.Groups = append([]service.CapabilityPublicationGroup(nil), stored.Groups...)
	for i := range in.Groups {
		if in.Groups[i].ID == 0 {
			stored.Groups[i].ID = 0
		}
	}
	a, _ := json.Marshal(in)
	b, _ := json.Marshal(stored)
	return string(a) == string(b)
}

func publicationTxLimits(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '5s'`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout = '30s'`)
	return err
}

func publicationDBError(err error) error {
	if err == nil {
		return nil
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "40001", "40P01", "55P03", "23505", "57014":
			return service.ErrCapabilityPublicationConflict
		}
	}
	return err
}

type publicationGroupRow struct {
	ID                    int64                                     `json:"id"`
	Name                  string                                    `json:"name"`
	Platform              string                                    `json:"platform"`
	WirePlatform          string                                    `json:"wire_platform"`
	ProviderProfile       string                                    `json:"provider_profile"`
	RateMultiplier        float64                                   `json:"rate_multiplier"`
	Status                string                                    `json:"status"`
	IsExclusive           bool                                      `json:"is_exclusive"`
	ModelAllowlist        service.GroupModelAllowlist               `json:"model_allowlist"`
	ManagedModelRoutes    domain.ManagedModelRoutesConfig           `json:"managed_model_routes"`
	ModelPricing          []service.ChannelModelPricing             `json:"model_pricing"`
	MessagesDispatch      service.OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config"`
	DefaultMappedModel    string                                    `json:"default_mapped_model"`
	AllowMessagesDispatch bool                                      `json:"allow_messages_dispatch"`
}

func (row publicationGroupRow) group() *service.Group {
	return &service.Group{ID: row.ID, Name: row.Name, Platform: row.Platform, WirePlatform: row.WirePlatform, ProviderProfile: row.ProviderProfile, RateMultiplier: row.RateMultiplier, Status: row.Status, IsExclusive: row.IsExclusive, ModelAllowlist: row.ModelAllowlist, ManagedModelRoutes: row.ManagedModelRoutes, ModelPricing: row.ModelPricing, MessagesDispatchModelConfig: row.MessagesDispatch, DefaultMappedModel: row.DefaultMappedModel, AllowMessagesDispatch: row.AllowMessagesDispatch}
}

func (r *accountCapabilityPublicationRepository) snapshot(ctx context.Context, tx *sql.Tx, req service.CapabilityPublicationRequest, reserved map[int64]bool) (*service.CapabilityPublicationSnapshot, error) {
	snap := &service.CapabilityPublicationSnapshot{Request: req, Accounts: map[int64]*service.CapabilityPublicationAccountSnapshot{}, Groups: map[int64]*service.CapabilityPublicationGroupSnapshot{}, Evidence: map[int64]service.CapabilityPublicationEvidence{}}
	snap.Request.Groups = append([]service.CapabilityPublicationGroup(nil), req.Groups...)
	groupsRaw := map[int64]json.RawMessage{}
	groupIDs := []int64{}
	newGroups := map[int64]bool{}
	for i, input := range snap.Request.Groups {
		if input.ID == 0 || reserved[input.ID] {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE name=$1 AND deleted_at IS NULL)`, input.Name).Scan(&exists); err != nil {
				return nil, err
			}
			if exists {
				return nil, service.ErrCapabilityPublicationConflict
			}
			if input.ID == 0 {
				if err := tx.QueryRowContext(ctx, `SELECT nextval(pg_get_serial_sequence('groups','id'))`).Scan(&input.ID); err != nil {
					return nil, err
				}
				snap.Request.Groups[i].ID = input.ID
			}
			newGroups[input.ID] = true
			group := &service.Group{ID: input.ID, Name: input.Name, Platform: input.Platform, WirePlatform: input.Platform, RateMultiplier: input.RateMultiplier, Status: service.StatusActive}
			snap.Groups[input.ID] = &service.CapabilityPublicationGroupSnapshot{Group: group, IsNew: true, Bindings: map[int64]int{}}
			groupsRaw[input.ID], _ = json.Marshal(group)
		}
		groupIDs = append(groupIDs, input.ID)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	rows, err := tx.QueryContext(ctx, `SELECT to_jsonb(g)-'updated_at' FROM groups g WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		var row publicationGroupRow
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &row); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if newGroups[row.ID] {
			_ = rows.Close()
			return nil, service.ErrCapabilityPublicationConflict
		}
		snap.Groups[row.ID] = &service.CapabilityPublicationGroupSnapshot{Group: row.group(), Bindings: map[int64]int{}}
		groupsRaw[row.ID] = raw
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(snap.Groups) != len(groupIDs) {
		return nil, service.ErrCapabilityPublicationConflict
	}
	accountSet := map[int64]bool{}
	for _, id := range req.Scope.AccountIDs {
		accountSet[id] = true
	}
	for _, id := range req.DetachAccountIDs {
		accountSet[id] = true
	}
	for _, gs := range snap.Groups {
		for _, route := range gs.Group.ManagedModelRoutes.Routes {
			for _, branch := range service.ManagedModelRouteBranches(route) {
				for _, member := range branch.Accounts {
					accountSet[member.AccountID] = true
				}
			}
		}
	}
	// Group locks stop concurrent compliant binding inserts; lock existing edges
	// as well. Serializable isolation catches legacy writers using other paths.
	rows, err = tx.QueryContext(ctx, `SELECT account_id,group_id,priority FROM account_groups WHERE group_id=ANY($1) ORDER BY group_id,account_id FOR UPDATE`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var aid, gid int64
		var priority int
		if err = rows.Scan(&aid, &gid, &priority); err != nil {
			_ = rows.Close()
			return nil, err
		}
		accountSet[aid] = true
		snap.Groups[gid].Bindings[aid] = priority
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	accountIDs := publicationSortedIDs(accountSet)
	rows, err = tx.QueryContext(ctx, `SELECT id,name,platform,wire_platform,provider_profile,type,credentials,extra,proxy_id,management_folder_id,parent_account_id,schedulable,status FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &service.Account{}
		var credentials, extra []byte
		if err = rows.Scan(&a.ID, &a.Name, &a.Platform, &a.WirePlatform, &a.ProviderProfile, &a.Type, &credentials, &extra, &a.ProxyID, &a.ManagementFolderID, &a.ParentAccountID, &a.Schedulable, &a.Status); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(credentials, &a.Credentials); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(extra, &a.Extra); err != nil {
			_ = rows.Close()
			return nil, err
		}
		snap.Accounts[a.ID] = &service.CapabilityPublicationAccountSnapshot{Account: a, Bindings: map[int64]int{}}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(snap.Accounts) != len(accountIDs) {
		return nil, service.ErrCapabilityPublicationConflict
	}
	proxyIDs := map[int64]bool{}
	for _, as := range snap.Accounts {
		if as.Account.ProxyID != nil {
			proxyIDs[*as.Account.ProxyID] = true
		}
	}
	if len(proxyIDs) > 0 {
		proxyRows, proxyErr := tx.QueryContext(ctx, `SELECT id,name,protocol,host,port,COALESCE(username,''),COALESCE(password,''),status FROM proxies WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR SHARE`, pq.Array(publicationSortedIDs(proxyIDs)))
		if proxyErr != nil {
			return nil, proxyErr
		}
		proxies := map[int64]*service.Proxy{}
		for proxyRows.Next() {
			p := &service.Proxy{}
			if proxyErr = proxyRows.Scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.Password, &p.Status); proxyErr != nil {
				_ = proxyRows.Close()
				return nil, proxyErr
			}
			proxies[p.ID] = p
		}
		proxyErr = proxyRows.Err()
		_ = proxyRows.Close()
		if proxyErr != nil {
			return nil, proxyErr
		}
		for _, as := range snap.Accounts {
			if as.Account.ProxyID != nil {
				as.Account.Proxy = proxies[*as.Account.ProxyID]
				if as.Account.Proxy == nil {
					return nil, service.ErrCapabilityPublicationConflict
				}
			}
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT account_id,group_id,priority FROM account_groups WHERE account_id=ANY($1) ORDER BY account_id,group_id FOR UPDATE`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var aid, gid int64
		var priority int
		if err = rows.Scan(&aid, &gid, &priority); err != nil {
			_ = rows.Close()
			return nil, err
		}
		snap.Accounts[aid].Bindings[gid] = priority
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for _, gid := range groupIDs {
		if newGroups[gid] {
			continue
		}
		if err = publicationLoadGroupRelations(ctx, tx, snap.Groups[gid]); err != nil {
			return nil, err
		}
	}
	ids := map[int64]bool{}
	for _, group := range req.Groups {
		for _, model := range group.Models {
			for _, id := range model.EvidenceIDs {
				ids[id] = true
			}
		}
	}
	for _, id := range req.SchedulingEvidenceIDs {
		ids[id] = true
	}
	rows, err = tx.QueryContext(ctx, `SELECT i.id,i.account_id,i.folder_id,i.config_fingerprint,i.upstream_model,i.protocol,i.profile,i.status,i.result,i.finished_at,r.kind,r.folder_ids,r.account_ids,`+capabilityPublicationSupersededSQL+`
		FROM admin_capability_items i JOIN admin_capability_runs r ON r.id=i.run_id WHERE i.id=ANY($1) ORDER BY i.id FOR SHARE OF i,r`, pq.Array(publicationSortedIDs(ids)))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e service.CapabilityPublicationEvidence
		var folders, accounts []byte
		if err = rows.Scan(&e.ID, &e.AccountID, &e.FolderID, &e.ConfigFingerprint, &e.UpstreamModel, &e.Protocol, &e.Profile, &e.Status, &e.Result, &e.FinishedAt, &e.RunKind, &folders, &accounts, &e.Superseded); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(folders, &e.RunFolderIDs); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(accounts, &e.RunAccountIDs); err != nil {
			_ = rows.Close()
			return nil, err
		}
		snap.Evidence[e.ID] = e
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(snap.Evidence) != len(ids) {
		return nil, service.ErrCapabilityPublicationInvalid
	}
	accountsCAS := map[int64]any{}
	for id, as := range snap.Accounts {
		a := as.Account
		accountsCAS[id] = map[string]any{"fingerprint": service.ManagedModelAccountFingerprint(a), "folder_id": a.ManagementFolderID, "parent_account_id": a.ParentAccountID, "schedulable": a.Schedulable, "status": a.Status, "model_mapping": a.GetModelMapping(), "bindings": as.Bindings}
	}
	groupRelations := map[int64]any{}
	for id, gs := range snap.Groups {
		groupRelations[id] = map[string]any{"channel": gs.Channel, "routes": gs.Routes, "bindings": gs.Bindings}
	}
	raw, err := json.Marshal(map[string]any{"groups": groupsRaw, "group_relations": groupRelations, "accounts": accountsCAS, "evidence": snap.Evidence})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	snap.Fingerprint = hex.EncodeToString(hash[:])
	return snap, nil
}

func publicationLoadGroupRelations(ctx context.Context, tx *sql.Tx, gs *service.CapabilityPublicationGroupSnapshot) error {
	var channelID int64
	err := tx.QueryRowContext(ctx, `SELECT channel_id FROM channel_groups WHERE group_id=$1 FOR UPDATE`, gs.Group.ID).Scan(&channelID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		ch := &service.Channel{}
		var mapping, features []byte
		err = tx.QueryRowContext(ctx, `SELECT id,name,COALESCE(description,''),status,billing_model_source,restrict_models,COALESCE(features,''),features_config,apply_pricing_to_account_stats,model_mapping FROM channels WHERE id=$1 FOR UPDATE`, channelID).Scan(&ch.ID, &ch.Name, &ch.Description, &ch.Status, &ch.BillingModelSource, &ch.RestrictModels, &ch.Features, &features, &ch.ApplyPricingToAccountStats, &mapping)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(mapping, &ch.ModelMapping); err != nil {
			return err
		}
		if err = json.Unmarshal(features, &ch.FeaturesConfig); err != nil {
			return err
		}
		var pricing, stats []byte
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(p)||jsonb_build_object('intervals',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.sort_order,i.id) FROM channel_pricing_intervals i WHERE i.pricing_id=p.id),'[]'::jsonb)) ORDER BY p.id),'[]'::jsonb) FROM channel_model_pricing p WHERE p.channel_id=$1`, channelID).Scan(&pricing)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(pricing, &ch.ModelPricing); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('ID',r.id,'ChannelID',r.channel_id,'Name',r.name,'GroupIDs',r.group_ids,'AccountIDs',r.account_ids,'SortOrder',r.sort_order,'Pricing',COALESCE((SELECT jsonb_agg(to_jsonb(p)||jsonb_build_object('intervals',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.sort_order,i.id) FROM channel_account_stats_pricing_intervals i WHERE i.pricing_id=p.id),'[]'::jsonb)) ORDER BY p.id) FROM channel_account_stats_model_pricing p WHERE p.rule_id=r.id),'[]'::jsonb)) ORDER BY r.sort_order,r.id),'[]'::jsonb) FROM channel_account_stats_pricing_rules r WHERE r.channel_id=$1`, channelID).Scan(&stats)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(stats, &ch.AccountStatsPricingRules); err != nil {
			return err
		}
		gs.Channel = ch
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,group_id,public_model,match_type,target_platform,upstream_model,endpoint,priority,enabled,COALESCE(notes,''),created_at,updated_at FROM composite_model_routes WHERE group_id=$1 AND deleted_at IS NULL ORDER BY priority,id FOR UPDATE`, gs.Group.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	gs.Routes = []service.CompositeModelRoute{}
	for rows.Next() {
		var route service.CompositeModelRoute
		if err = rows.Scan(&route.ID, &route.GroupID, &route.PublicModel, &route.MatchType, &route.TargetPlatform, &route.UpstreamModel, &route.Endpoint, &route.Priority, &route.Enabled, &route.Notes, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return err
		}
		gs.Routes = append(gs.Routes, route)
	}
	return rows.Err()
}

func publicationSortedIDs(set map[int64]bool) []int64 {
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func publicationApplyGroup(ctx context.Context, tx *sql.Tx, gp service.CapabilityPublicationGroupPatch) error {
	if gp.Create {
		_, err := tx.ExecContext(ctx, `INSERT INTO groups(id,name,platform,wire_platform,provider_profile,rate_multiplier,is_exclusive,status,subscription_type) VALUES($1,$2,$3,$3,'',$4,false,$5,'standard')`, gp.GroupID, gp.Name, gp.Platform, gp.RateMultiplier, gp.Status)
		if err != nil {
			return err
		}
	}
	routes, err := json.Marshal(gp.ManagedModelRoutes)
	if err != nil {
		return err
	}
	allowlist, err := json.Marshal(gp.ModelAllowlist)
	if err != nil {
		return err
	}
	dispatch, err := json.Marshal(gp.MessagesDispatch)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE groups SET platform=$2,wire_platform=$2,managed_model_routes=$3::jsonb,model_allowlist=$4::jsonb,messages_dispatch_model_config=$5::jsonb,allow_messages_dispatch=$6,default_mapped_model=$8,status=$7,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, gp.GroupID, gp.Platform, string(routes), string(allowlist), string(dispatch), gp.AllowMessagesDispatch, gp.Status, gp.DefaultMappedModel)
	if err != nil {
		return err
	}
	channelName := fmt.Sprintf("public-capabilities-g%d", gp.GroupID)
	var channelID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM channels WHERE name=$1 FOR UPDATE`, channelName).Scan(&channelID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var onlyThisGroup bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_groups WHERE channel_id=$1 AND group_id=$2) AND NOT EXISTS(SELECT 1 FROM channel_groups WHERE channel_id=$1 AND group_id<>$2)`, channelID, gp.GroupID).Scan(&onlyThisGroup)
		if err != nil {
			return err
		}
		if !onlyThisGroup || channelID == 1 {
			return service.ErrCapabilityPublicationConflict
		}
	} else {
		err = tx.QueryRowContext(ctx, `INSERT INTO channels(name,description,status,billing_model_source,restrict_models,features,features_config) VALUES($1,'Managed public account capabilities','active','requested',false,'','{}'::jsonb) RETURNING id`, channelName).Scan(&channelID)
		if err != nil {
			return err
		}
	}
	mapping, err := json.Marshal(gp.ChannelMapping)
	if err != nil {
		return err
	}
	features, err := json.Marshal(gp.ChannelFeaturesConfig)
	if err != nil {
		return err
	}
	if string(features) == "null" {
		features = []byte(`{}`)
	}
	_, err = tx.ExecContext(ctx, `UPDATE channels SET model_mapping=$2::jsonb,billing_model_source='requested',restrict_models=false,features=$3,features_config=$4::jsonb,apply_pricing_to_account_stats=$5,updated_at=NOW() WHERE id=$1`, channelID, string(mapping), gp.ChannelFeatures, string(features), gp.ChannelApplyPricingToAccountStats)
	if err != nil {
		return err
	}
	// The helpers may assign generated IDs; deep-copy so the approved plan stays
	// immutable for retries and the audit retains its original price evidence.
	pricingJSON, _ := json.Marshal(gp.ChannelPricing)
	var pricing []service.ChannelModelPricing
	_ = json.Unmarshal(pricingJSON, &pricing)
	if err = replaceModelPricingTx(ctx, tx, channelID, pricing); err != nil {
		return err
	}
	statsJSON, _ := json.Marshal(gp.ChannelAccountStatsPricingRules)
	var stats []service.AccountStatsPricingRule
	_ = json.Unmarshal(statsJSON, &stats)
	if err = replaceAccountStatsPricingRulesTx(ctx, tx, channelID, stats); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_groups(channel_id,group_id) VALUES($1,$2) ON CONFLICT(group_id) DO UPDATE SET channel_id=EXCLUDED.channel_id`, channelID, gp.GroupID)
	if err != nil {
		return err
	}
	// Preserve unchanged manual routes (including identity, endpoint, priority
	// and disabled state). Only routes absent from the frozen merge are retired.
	retainedRouteIDs := make([]int64, 0, len(gp.CompositeRoutes))
	for _, route := range gp.CompositeRoutes {
		if route.ID > 0 {
			retainedRouteIDs = append(retainedRouteIDs, route.ID)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE composite_model_routes SET deleted_at=NOW(),updated_at=NOW() WHERE group_id=$1 AND deleted_at IS NULL AND NOT (id=ANY($2))`, gp.GroupID, pq.Array(retainedRouteIDs))
	if err != nil {
		return err
	}
	for _, route := range gp.CompositeRoutes {
		if route.ID > 0 {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO composite_model_routes(group_id,public_model,match_type,target_platform,upstream_model,endpoint,priority,enabled,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, gp.GroupID, route.PublicModel, route.MatchType, route.TargetPlatform, route.UpstreamModel, route.Endpoint, route.Priority, route.Enabled, route.Notes)
		if err != nil {
			return err
		}
	}
	return nil
}

func publicationApplyAccount(ctx context.Context, tx *sql.Tx, ap service.CapabilityPublicationAccountPatch) error {
	if len(ap.ModelMapping) > 0 || len(ap.RemoveSelectors) > 0 {
		appendMappings := ap.ModelMapping
		if appendMappings == nil {
			appendMappings = map[string]string{}
		}
		mapping, err := json.Marshal(appendMappings)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}',(((CASE WHEN jsonb_typeof(credentials->'model_mapping')='object' THEN credentials->'model_mapping' ELSE '{}'::jsonb END)-COALESCE($2::text[],'{}'::text[]))||$3::jsonb),true),updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, ap.AccountID, pq.Array(ap.RemoveSelectors), string(mapping))
		if err != nil {
			return err
		}
	}
	for _, gid := range ap.RemoveGroupIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, ap.AccountID, gid); err != nil {
			return err
		}
	}
	for _, gid := range ap.AddGroupIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,50) ON CONFLICT(account_id,group_id) DO NOTHING`, ap.AccountID, gid); err != nil {
			return err
		}
	}
	if ap.Schedulable != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET schedulable=$2,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, ap.AccountID, *ap.Schedulable); err != nil {
			return err
		}
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &ap.AccountID, nil, nil); err != nil {
		return err
	}
	if len(ap.AddGroupIDs) > 0 || len(ap.RemoveGroupIDs) > 0 {
		groupIDs := append(append([]int64{}, ap.AddGroupIDs...), ap.RemoveGroupIDs...)
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &ap.AccountID, nil, map[string]any{"group_ids": groupIDs}); err != nil {
			return err
		}
	}
	return nil
}
