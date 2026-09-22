package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (r *pluginRepository) ReadExtensionState(ctx context.Context, plugin string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	var out extensionv1.StateResult
	err := r.db.QueryRowContext(ctx, `SELECT revision,value FROM sub2api_plugin_state WHERE plugin_key=$1 AND namespace=$2 AND state_key=$3`, plugin, req.Namespace, req.Key).Scan(&out.Revision, &out.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	out.Found = err == nil
	return out, err
}

func (r *pluginRepository) CompareSwapExtensionState(ctx context.Context, plugin string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	var out extensionv1.StateResult
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockPluginExecution(ctx, tx, plugin); err != nil {
		return out, err
	}
	var projectionData map[string]any
	if req.Projection != nil {
		projectionData, err = lockPluginAccountProjection(ctx, tx, plugin, req.Projection)
		if err != nil {
			return out, err
		}
	}
	var row *sql.Row
	if req.ExpectedRevision == 0 {
		row = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_state(plugin_key,namespace,state_key,value,next_at)
			VALUES($1,$2,$3,$4::jsonb,$5) ON CONFLICT DO NOTHING RETURNING revision,value`, plugin, req.Namespace, req.Key, []byte(req.Value), req.NextAt)
	} else {
		row = tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_state SET value=$4::jsonb,revision=revision+1,updated_at=NOW(),next_at=$6
			WHERE plugin_key=$1 AND namespace=$2 AND state_key=$3 AND revision=$5 RETURNING revision,value`, plugin, req.Namespace, req.Key, []byte(req.Value), req.ExpectedRevision, req.NextAt)
	}
	err = row.Scan(&out.Revision, &out.Value)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return r.ReadExtensionState(ctx, plugin, req)
	}
	if err != nil {
		return out, err
	}
	if req.Projection != nil {
		raw, marshalErr := json.Marshal(projectionData)
		if marshalErr != nil {
			return out, marshalErr
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(NULLIF(extra,'null'::jsonb),'{}'::jsonb),ARRAY[$2],$3::jsonb,true),updated_at=NOW() WHERE id=$1`, req.Projection.AccountID, service.PluginAccountProjectionKey, raw); err != nil {
			return out, err
		}
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &req.Projection.AccountID, nil, nil); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Found, out.Applied = err == nil, err == nil
	if req.Projection != nil && r.refreshAccount != nil {
		r.refreshAccount(ctx, req.Projection.AccountID)
	}
	return out, err
}

func lockPluginAccountProjection(ctx context.Context, tx *sql.Tx, plugin string, projection *extensionv1.AccountProjection) (map[string]any, error) {
	var platform, accountType string
	var credentials, extra []byte
	if err := tx.QueryRowContext(ctx, `SELECT platform,type,credentials,extra FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, projection.AccountID).Scan(&platform, &accountType, &credentials, &extra); err != nil {
		return nil, err
	}
	a := &service.Account{ID: projection.AccountID, Platform: platform, Type: accountType}
	if json.Unmarshal(credentials, &a.Credentials) != nil || json.Unmarshal(extra, &a.Extra) != nil {
		return nil, errors.New("invalid account data for projection")
	}
	if service.CodexTicketAccountIdentity(a) != projection.Identity {
		return nil, errors.New("credential owner changed")
	}
	values, _ := a.Extra[service.PluginAccountProjectionKey].(map[string]any)
	if values == nil {
		values = make(map[string]any)
	}
	current := service.PluginAccountProjection{Identity: projection.Identity, Scheduling: make(map[string]extensionv1.SchedulingConstraint)}
	if raw, err := json.Marshal(values[plugin]); err == nil {
		var previous service.PluginAccountProjection
		if json.Unmarshal(raw, &previous) == nil && previous.Identity == projection.Identity && previous.Scheduling != nil {
			current = previous
		}
	}
	for _, constraint := range projection.Scheduling {
		current.Scheduling[constraint.Model] = constraint
	}
	if current.Observations == nil {
		current.Observations = make(map[string]extensionv1.AccountObservation)
	}
	for _, observation := range projection.Observations {
		current.Observations[observation.Key] = observation
	}
	values[plugin] = current
	return values, nil
}

func (r *pluginRepository) DueExtensionStates(ctx context.Context, plugin string, req extensionv1.DueStateRequest) ([]extensionv1.DueState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT state_key,revision,value FROM sub2api_plugin_state WHERE plugin_key=$1 AND namespace=$2 AND next_at<=NOW() ORDER BY next_at,state_key LIMIT $3`, plugin, req.Namespace, req.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]extensionv1.DueState, 0)
	for rows.Next() {
		var row extensionv1.DueState
		if err := rows.Scan(&row.Key, &row.Revision, &row.Value); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *pluginRepository) AcquireExtensionLease(ctx context.Context, plugin string, req extensionv1.LeaseRequest) (extensionv1.LeaseResult, error) {
	var out extensionv1.LeaseResult
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockPluginExecution(ctx, tx, plugin); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_leases(plugin_key,namespace,lease_key,owner,expires_at)
		VALUES($1,$2,$3,$4,NOW()+$5*INTERVAL '1 second')
		ON CONFLICT(plugin_key,namespace,lease_key) DO UPDATE
		SET owner=EXCLUDED.owner,generation=sub2api_plugin_leases.generation+1,expires_at=EXCLUDED.expires_at
		WHERE sub2api_plugin_leases.expires_at<=NOW()
		RETURNING generation,expires_at`, plugin, req.Namespace, req.Key, req.Owner, req.TTLSeconds).Scan(&out.Generation, &out.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	out.Acquired = err == nil
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (r *pluginRepository) ReleaseExtensionLease(ctx context.Context, plugin string, req extensionv1.LeaseRequest) (extensionv1.LeaseResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return extensionv1.LeaseResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockPluginExecution(ctx, tx, plugin); err != nil {
		return extensionv1.LeaseResult{}, err
	}
	// Keep the row and generation as a fencing token; deleting it would allow ABA.
	_, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_leases SET expires_at=NOW()
		WHERE plugin_key=$1 AND namespace=$2 AND lease_key=$3 AND owner=$4 AND generation=$5`, plugin, req.Namespace, req.Key, req.Owner, req.Generation)
	if err != nil {
		return extensionv1.LeaseResult{}, err
	}
	return extensionv1.LeaseResult{}, tx.Commit()
}
