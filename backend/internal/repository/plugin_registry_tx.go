package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/jackc/pgx/v5/pgconn"
)

// Only desired-graph mutations participate. Runtime health/config publication
// and business reads do not acquire a registry-wide lock or read the graph.
func (r *pluginRepository) beginPluginRegistryTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	return tx, pluginRegistryTxError(err)
}

func pluginRegistryTxError(err error) error {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && postgres.Code == "40001" {
		return service.ErrPluginStateChanged
	}
	return err
}

func rollbackPluginRegistryTx(tx *sql.Tx, resultErr *error) {
	_ = tx.Rollback()
	*resultErr = pluginRegistryTxError(*resultErr)
}

// Read both graph tables through this transaction, including our own writes.
// PostgreSQL SSI then also tracks absent/overlapping owners and dependencies:
// target-only row locks or a List outside the transaction cannot do that.
func readPluginRegistryTx(ctx context.Context, tx *sql.Tx) ([]*service.PluginInstallation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.manifest,COALESCE(jsonb_agg(jsonb_build_object(
		'capability',b.capability,'platform',b.platform,'account_type',b.account_type,'enabled',b.enabled,'rollout_percent',b.rollout_percent)
		ORDER BY b.id) FILTER (WHERE b.id IS NOT NULL),'[]'::jsonb)
		FROM sub2api_plugin_installations p LEFT JOIN sub2api_plugin_bindings b ON b.plugin_id=p.id GROUP BY p.id ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var installations []*service.PluginInstallation
	for rows.Next() {
		installation := &service.PluginInstallation{}
		var manifest, bindings []byte
		if err := rows.Scan(&installation.ID, &manifest, &bindings); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(manifest, &installation.Manifest); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(bindings, &installation.Bindings); err != nil {
			return nil, err
		}
		installations = append(installations, installation)
	}
	return installations, rows.Err()
}

func commitPluginRegistryTx(ctx context.Context, tx *sql.Tx) error {
	installations, err := readPluginRegistryTx(ctx, tx)
	if err != nil {
		return pluginRegistryTxError(err)
	}
	// Desired bindings, not process health, define the graph. An error or an
	// updating process may remain temporarily unavailable without losing intent.
	if err := service.ValidatePluginRegistry(installations); err != nil {
		return err
	}
	return pluginRegistryTxError(tx.Commit())
}
