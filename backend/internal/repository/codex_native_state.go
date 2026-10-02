package repository

import (
	"context"
	"database/sql"
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func (r *nativeCodexRepository) ReadExtensionState(ctx context.Context, plugin string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	var out extensionv1.StateResult
	err := r.db.QueryRowContext(ctx, `SELECT revision,value FROM sub2api_plugin_state WHERE plugin_key=$1 AND namespace=$2 AND state_key=$3`, plugin, req.Namespace, req.Key).Scan(&out.Revision, &out.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	out.Found = err == nil
	return out, err
}

func (r *nativeCodexRepository) CompareSwapExtensionState(ctx context.Context, plugin string, req extensionv1.StateRequest) (extensionv1.StateResult, error) {
	var out extensionv1.StateResult
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockNativeCodexExecution(ctx, tx, plugin); err != nil {
		return out, err
	}
	var row *sql.Row
	if req.ExpectedRevision == 0 {
		row = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_state(plugin_key,namespace,state_key,value)
			VALUES($1,$2,$3,$4::jsonb) ON CONFLICT DO NOTHING RETURNING revision,value`, plugin, req.Namespace, req.Key, []byte(req.Value))
	} else {
		row = tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_state SET value=$4::jsonb,revision=revision+1,updated_at=NOW()
			WHERE plugin_key=$1 AND namespace=$2 AND state_key=$3 AND revision=$5 RETURNING revision,value`, plugin, req.Namespace, req.Key, []byte(req.Value), req.ExpectedRevision)
	}
	err = row.Scan(&out.Revision, &out.Value)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return r.ReadExtensionState(ctx, plugin, req)
	}
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Found, out.Applied = err == nil, err == nil
	return out, err
}
