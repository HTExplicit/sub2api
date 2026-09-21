package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func pluginRuntimeLockName(id int64) string { return fmt.Sprintf("sub2api-plugin-runtime:%d", id) }

// Each live process holds a shared advisory lock on a dedicated connection.
// Replacement requires the exclusive lock, so another host cannot promote a
// new generation while an old process still owns credentials or executions.
func (r *pluginRepository) HoldPluginRuntime(ctx context.Context, installation *service.PluginInstallation) (func(), error) {
	pooled, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var config *pgx.ConnConfig
	err = pooled.Raw(func(raw any) error {
		conn, ok := raw.(*stdlib.Conn)
		if !ok {
			return errors.New("plugin runtime leases require PostgreSQL")
		}
		config = conn.Conn().Config().Copy()
		return nil
	})
	_ = pooled.Close()
	if err != nil {
		return nil, err
	}
	// A process-lifetime session must not occupy the business query pool: a
	// small configured pool would deadlock during startup or package replacement.
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["application_name"] = "sub2api-plugin-lease"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared(hashtextextended($1,0))`, pluginRuntimeLockName(installation.ID)).Scan(&locked)
	if err != nil || !locked {
		_ = conn.Close(ctx)
		if err == nil {
			err = service.ErrPluginUpdateWaiting
		}
		return nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Closing the dedicated session releases all its advisory locks.
			_ = conn.Close(unlockCtx)
		})
	}
	var generation, revision int64
	var state, digest, configEncrypted string
	err = conn.QueryRow(ctx, `SELECT runtime_generation,state,package_sha256,revision,config_encrypted FROM sub2api_plugin_installations WHERE id=$1`, installation.ID).Scan(&generation, &state, &digest, &revision, &configEncrypted)
	stale := err != nil || generation != installation.RuntimeGeneration || digest != installation.PackageSHA256 || state == service.PluginStateUpdating
	if !stale && service.PluginBusinessIOLeaseRequired(ctx) {
		// Business IO must be admitted against the exact current policy intent.
		// Process-lifetime leases deliberately retain the older startup and
		// disabled-plugin diagnostics contract above.
		stale = state != service.PluginStateEnabled || revision != installation.Revision || configEncrypted != installation.ConfigEncrypted
	}
	if stale {
		release()
		if err == nil {
			err = service.ErrPluginStateChanged
		}
		return nil, err
	}
	return release, nil
}

func (r *pluginRepository) StagePluginUpdate(ctx context.Context, previous, candidate *service.PluginInstallation, policy string) error {
	if policy != service.PluginUpdateBundled && policy != service.PluginUpdatePinned {
		return errors.New("invalid plugin update policy")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	// The disabled rows of an unfinished bundle are temporary, not activation
	// intent. Explicit pinned installs no longer belong to that old journal.
	err = tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_installations p SET state='updating',last_error='',updated_at=NOW()
		WHERE id=$1 AND revision=$2 AND package_sha256=$3 AND state<>'updating'
		AND (update_policy='pinned' OR NOT EXISTS (
			SELECT 1 FROM sub2api_plugin_bootstrap b WHERE b.plugin_key=p.plugin_key AND (NOT b.completed OR NOT b.state_imported)
		))
		RETURNING runtime_generation`, previous.ID, previous.Revision, previous.PackageSHA256).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPluginStateChanged
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_updates(plugin_id,artifact_data,package_sha256,source_generation,update_policy)
		VALUES($1,$2,$3,$4,$5)`, previous.ID, candidate.ArtifactData, candidate.PackageSHA256, generation, policy)
	if err != nil {
		return err
	}
	if err = cancelPluginAccountJobs(ctx, tx, previous.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *pluginRepository) PendingPluginArtifact(ctx context.Context, id int64) ([]byte, error) {
	var artifact []byte
	err := r.db.QueryRowContext(ctx, `SELECT u.artifact_data FROM sub2api_plugin_updates u JOIN sub2api_plugin_installations p ON p.id=u.plugin_id WHERE p.id=$1 AND p.state='updating'`, id).Scan(&artifact)
	return artifact, err
}

func (r *pluginRepository) CommitPluginUpdate(ctx context.Context, previous, candidate *service.PluginInstallation) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, pluginRuntimeLockName(previous.ID)).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return service.ErrPluginUpdateWaiting
	}
	var policy string
	err = tx.QueryRowContext(ctx, `SELECT u.update_policy FROM sub2api_plugin_updates u JOIN sub2api_plugin_installations p ON p.id=u.plugin_id
		WHERE p.id=$1 AND p.revision=$2 AND p.runtime_generation=u.source_generation AND p.state='updating' AND u.package_sha256=$3
		FOR UPDATE OF p,u`, previous.ID, previous.Revision, candidate.PackageSHA256).Scan(&policy)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPluginStateChanged
	}
	if err != nil {
		return err
	}
	bindings := service.PluginReplacementBindings(previous.Bindings, candidate.Manifest)
	state := service.PluginStateDisabled
	for _, binding := range bindings {
		if binding.Enabled {
			state = service.PluginStateEnabled
		}
	}
	manifest, err := json.Marshal(candidate.Manifest)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_installations SET name=$2,version=$3,description=$4,author=$5,manifest=$6::jsonb,
		artifact_data=$7,artifact_path=$8,install_path=$9,binary_path=$10,binary_sha256=$11,package_sha256=$12,
		signature_status=$13,update_policy=$14,state=$15,runtime_generation=runtime_generation+1,last_error='',updated_at=NOW()
		WHERE id=$1`, previous.ID, candidate.Name, candidate.Version, candidate.Description, candidate.Author, manifest, candidate.ArtifactData,
		candidate.ArtifactPath, candidate.InstallPath, candidate.BinaryPath, candidate.BinarySHA256, candidate.PackageSHA256, candidate.SignatureStatus, policy, state)
	if err != nil {
		return err
	}
	if err = replacePluginBindings(ctx, tx, previous.ID, bindings); err != nil {
		return err
	}
	// Keep the original one-time import journal. An update never replays a seed.
	raw, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_bootstrap SET desired_bindings=$2::jsonb,desired_enabled=$3,updated_at=NOW() WHERE plugin_key=$1`, previous.PluginKey, raw, state == service.PluginStateEnabled)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sub2api_plugin_updates WHERE plugin_id=$1`, previous.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *pluginRepository) SetPluginUpdatePolicy(ctx context.Context, id, revision int64, policy string) error {
	if policy != service.PluginUpdateBundled && policy != service.PluginUpdatePinned {
		return errors.New("invalid plugin update policy")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE sub2api_plugin_installations SET update_policy=$3,updated_at=NOW()
		WHERE id=$1 AND revision=$2 AND state<>'updating'`, id, revision, policy)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows != 1 {
		return service.ErrPluginStateChanged
	}
	return err
}

// State publication shares the installation lock with package/config changes.
// A stale process cannot race a generation check and then commit its projection.
func lockPluginExecution(ctx context.Context, tx *sql.Tx, plugin string) error {
	execution, ok := service.PluginExecutionFromContext(ctx)
	if !ok {
		return nil
	}
	var generation int64
	var state string
	err := tx.QueryRowContext(ctx, `SELECT runtime_generation,state FROM sub2api_plugin_installations WHERE id=$1 AND plugin_key=$2 FOR SHARE`, execution.ID, plugin).Scan(&generation, &state)
	if err != nil {
		return err
	}
	if generation != execution.Generation || state == service.PluginStateUpdating {
		return service.ErrPluginStateChanged
	}
	return nil
}

var _ service.PluginUpdateRepository = (*pluginRepository)(nil)
var _ service.PluginRuntimeLocker = (*pluginRepository)(nil)

func (r *pluginRepository) CompletedBundledPlugin(ctx context.Context, key string) (bool, error) {
	var completed bool
	err := r.db.QueryRowContext(ctx, `SELECT completed AND state_imported FROM sub2api_plugin_bootstrap WHERE plugin_key=$1`, key).Scan(&completed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return completed, err
}
