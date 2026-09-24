package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type nativeFeatureBootstrapRepository struct{ db *sql.DB }

func NewNativeFeatureBootstrapRepository(db *sql.DB) service.NativeFeatureBootstrapRepository {
	return &nativeFeatureBootstrapRepository{db: db}
}

const nativeRetirementInstallationsSQL = `SELECT id,plugin_key,state,config_encrypted,runtime_generation,manifest
	FROM sub2api_plugin_installations WHERE plugin_key IN
	('codexrip.account-tools','codexrip.admin-observability','codexrip.cindy-provider',
	 'codexrip.codex-runtime','codexrip.image-tools','codexrip.model-policy','codexrip.prompt-skills')
	ORDER BY id FOR UPDATE`

func (r *nativeFeatureBootstrapRepository) RetireNativeFeatures(ctx context.Context, convert func(service.NativeRetirementPlugin) (map[string]json.RawMessage, error)) (*service.NativeRetirementSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, service.NativeFeatureRetirementSetting); err != nil {
		return nil, err
	}
	var saved string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, service.NativeFeatureRetirementSetting).Scan(&saved)
	if err == nil {
		var snapshot service.NativeRetirementSnapshot
		if json.Unmarshal([]byte(saved), &snapshot) != nil || snapshot.Version != 1 || !snapshot.Completed || snapshot.Plugins == nil {
			return nil, errors.New("invalid first-party retirement receipt")
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return &snapshot, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, nativeRetirementInstallationsSQL)
	if err != nil {
		return nil, err
	}
	var plugins []service.NativeRetirementPlugin
	for rows.Next() {
		var plugin service.NativeRetirementPlugin
		if err = rows.Scan(&plugin.ID, &plugin.Key, &plugin.State, &plugin.ConfigEncrypted, &plugin.RuntimeGeneration, &plugin.Manifest); err != nil {
			_ = rows.Close()
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}

	snapshot := &service.NativeRetirementSnapshot{Version: 1, Completed: true, RetiredAt: time.Now().UTC(), Plugins: make(map[string]service.NativeRetirementPlugin)}
	for _, plugin := range plugins {
		// This is the same lock held by running legacy processes and native
		// business IO. A live owner must finish before retirement can commit.
		var acquired bool
		if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("sub2api-plugin-runtime:%d", plugin.ID)).Scan(&acquired); err != nil {
			return nil, err
		}
		if !acquired {
			return nil, fmt.Errorf("%s still has a running operation", plugin.Key)
		}
		bindings, readErr := readNativeRetirementBindings(ctx, tx, plugin.ID)
		if readErr != nil {
			return nil, readErr
		}
		plugin.Bindings = bindings
		var bootstrap []byte
		readErr = tx.QueryRowContext(ctx, `SELECT to_jsonb(b) FROM sub2api_plugin_bootstrap b WHERE plugin_key=$1 FOR UPDATE`, plugin.Key).Scan(&bootstrap)
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return nil, readErr
		}
		if readErr == nil {
			plugin.Bootstrap = append(json.RawMessage(nil), bootstrap...)
		}
		settings, conversionErr := convert(plugin)
		if conversionErr != nil {
			return nil, conversionErr
		}
		keys := make([]string, 0, len(settings))
		for key := range settings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var existing string
			readErr = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, key).Scan(&existing)
			if readErr == nil {
				if validationErr := service.ValidateNativeFeatureSetting(key, []byte(existing)); validationErr != nil {
					return nil, fmt.Errorf("invalid existing native setting %s", key)
				}
				continue
			}
			if !errors.Is(readErr, sql.ErrNoRows) {
				return nil, readErr
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO NOTHING`, key, string(settings[key])); err != nil {
				return nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_bindings SET enabled=false,updated_at=NOW() WHERE plugin_id=$1 AND enabled`, plugin.ID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_installations
			SET state='disabled',runtime_generation=runtime_generation+CASE WHEN plugin_key='codexrip.codex-runtime' THEN 1 ELSE 0 END,updated_at=NOW()
			WHERE id=$1`, plugin.ID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_bootstrap SET user_removed=true,updated_at=NOW() WHERE plugin_key=$1`, plugin.Key); err != nil {
			return nil, err
		}
		plugin.ConfigEncrypted, plugin.Manifest = "", nil
		snapshot.Plugins[plugin.Key] = plugin
	}
	if _, exists := snapshot.Plugins[service.NativeCodexPluginKey]; !exists {
		// Fresh databases have no retired package. This row is only a disabled
		// state anchor; it has no artifact, binary, signature, or active binding.
		var id int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_installations
			(plugin_key,name,version,description,manifest,artifact_path,install_path,binary_path,binary_sha256,state,runtime_generation)
			VALUES($1,'Native Codex state anchor','0.0.0','Persistent native runtime generation; no plugin executable','{}'::jsonb,'','','','','disabled',1)
			RETURNING id`, service.NativeCodexPluginKey).Scan(&id); err != nil {
			return nil, err
		}
		snapshot.Plugins[service.NativeCodexPluginKey] = service.NativeRetirementPlugin{
			ID: id, Key: service.NativeCodexPluginKey, State: "disabled", RuntimeGeneration: 1, NativeCreated: true,
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW())`, service.NativeFeatureRetirementSetting, string(encoded)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func readNativeRetirementBindings(ctx context.Context, tx *sql.Tx, id int64) ([]service.NativeRetirementBinding, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,capability,platform,account_type,enabled,rollout_percent FROM sub2api_plugin_bindings WHERE plugin_id=$1 ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]service.NativeRetirementBinding, 0)
	for rows.Next() {
		var binding service.NativeRetirementBinding
		if err := rows.Scan(&binding.ID, &binding.Capability, &binding.Platform, &binding.AccountType, &binding.Enabled, &binding.RolloutPercent); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}
