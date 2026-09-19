package repository

import (
	"context"
	"database/sql"
	"errors"

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
	var row *sql.Row
	if req.ExpectedRevision == 0 {
		row = r.db.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_state(plugin_key,namespace,state_key,value,next_at)
			VALUES($1,$2,$3,$4::jsonb,$5) ON CONFLICT DO NOTHING RETURNING revision,value`, plugin, req.Namespace, req.Key, []byte(req.Value), req.NextAt)
	} else {
		row = r.db.QueryRowContext(ctx, `UPDATE sub2api_plugin_state SET value=$4::jsonb,revision=revision+1,updated_at=NOW(),next_at=$6
			WHERE plugin_key=$1 AND namespace=$2 AND state_key=$3 AND revision=$5 RETURNING revision,value`, plugin, req.Namespace, req.Key, []byte(req.Value), req.ExpectedRevision, req.NextAt)
	}
	err := row.Scan(&out.Revision, &out.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return r.ReadExtensionState(ctx, plugin, req)
	}
	out.Found, out.Applied = err == nil, err == nil
	return out, err
}

func (r *pluginRepository) DueExtensionStates(ctx context.Context, plugin string, req extensionv1.DueStateRequest) ([]extensionv1.DueState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT state_key,revision,value FROM sub2api_plugin_state WHERE plugin_key=$1 AND namespace=$2 AND next_at<=NOW() ORDER BY next_at,state_key LIMIT $3`, plugin, req.Namespace, req.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	err := r.db.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_leases(plugin_key,namespace,lease_key,owner,expires_at)
		VALUES($1,$2,$3,$4,NOW()+$5*INTERVAL '1 second')
		ON CONFLICT(plugin_key,namespace,lease_key) DO UPDATE
		SET owner=EXCLUDED.owner,generation=sub2api_plugin_leases.generation+1,expires_at=EXCLUDED.expires_at
		WHERE sub2api_plugin_leases.expires_at<=NOW()
		RETURNING generation,expires_at`, plugin, req.Namespace, req.Key, req.Owner, req.TTLSeconds).Scan(&out.Generation, &out.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	out.Acquired = err == nil
	return out, err
}

func (r *pluginRepository) ReleaseExtensionLease(ctx context.Context, plugin string, req extensionv1.LeaseRequest) (extensionv1.LeaseResult, error) {
	// Keep the row and generation as a fencing token; deleting it would allow ABA.
	_, err := r.db.ExecContext(ctx, `UPDATE sub2api_plugin_leases SET expires_at=NOW()
		WHERE plugin_key=$1 AND namespace=$2 AND lease_key=$3 AND owner=$4 AND generation=$5`, plugin, req.Namespace, req.Key, req.Owner, req.Generation)
	return extensionv1.LeaseResult{}, err
}
