package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *pluginRepository) BundleApplied(ctx context.Context, key, bundle string) (bool, error) {
	var applied bool
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT user_removed OR (completed AND bundle_sha256=$2) FROM sub2api_plugin_bootstrap WHERE plugin_key=$1),false)
		OR EXISTS(SELECT 1 FROM sub2api_plugin_installations WHERE plugin_key=$1 AND (update_policy='pinned' OR state='updating'))`, key, bundle).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return applied, err
}

// Package replacement is staged with the previous activation intent recorded
// durably. A restart during migration cannot silently reset an enabled plugin.
func (r *pluginRepository) PrepareBundledPlugin(ctx context.Context, plugin *service.PluginInstallation, bundle, profile string, enabled bool, config string) (_ *service.PluginInstallation, resultErr error) {
	tx, err := r.beginPluginRegistryTx(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackPluginRegistryTx(tx, &resultErr)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "plugin-bundle:"+plugin.PluginKey); err != nil {
		return nil, err
	}
	var journalBundle string
	var journalEnabled bool
	var imported, complete, removed bool
	var oldProfile string
	var bindingSnapshot []byte
	err = tx.QueryRowContext(ctx, `SELECT bundle_sha256,desired_enabled,migration_profile,state_imported,completed,user_removed,desired_bindings FROM sub2api_plugin_bootstrap WHERE plugin_key=$1 FOR UPDATE`, plugin.PluginKey).Scan(&journalBundle, &journalEnabled, &oldProfile, &imported, &complete, &removed, &bindingSnapshot)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if removed || (journalBundle == bundle && complete) {
		return nil, nil
	}
	if oldProfile != profile {
		imported = false
	}
	var oldID int64
	var oldConfig string
	var oldPolicy string
	var oldState string
	var oldBindings []service.PluginBinding
	err = tx.QueryRowContext(ctx, `SELECT id,config_encrypted,update_policy,state FROM sub2api_plugin_installations WHERE plugin_key=$1 FOR UPDATE`, plugin.PluginKey).Scan(&oldID, &oldConfig, &oldPolicy, &oldState)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if oldID > 0 {
		if oldPolicy == service.PluginUpdatePinned || oldState == service.PluginStateUpdating {
			return nil, nil
		}
		if oldConfig != "" {
			config = oldConfig
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT capability,platform,account_type,enabled,rollout_percent FROM sub2api_plugin_bindings WHERE plugin_id=$1 ORDER BY id`, oldID)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			var binding service.PluginBinding
			if err = rows.Scan(&binding.Capability, &binding.Platform, &binding.AccountType, &binding.Enabled, &binding.RolloutPercent); err != nil {
				_ = rows.Close()
				return nil, err
			}
			oldBindings = append(oldBindings, binding)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if journalBundle != "" && !complete {
		if len(bindingSnapshot) == 0 || json.Unmarshal(bindingSnapshot, &oldBindings) != nil {
			return nil, errors.New("pending plugin migration has no valid binding intent")
		}
	}
	if journalBundle == bundle {
		enabled = journalEnabled
	} else if oldID > 0 {
		enabled = false
		for _, binding := range oldBindings {
			enabled = enabled || binding.Enabled
		}
	}
	manifest, err := json.Marshal(plugin.Manifest)
	if err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_installations(plugin_key,name,version,description,author,manifest,artifact_data,artifact_path,install_path,binary_path,binary_sha256,signature_status,state,config_encrypted,last_error,installed_at,updated_at,package_sha256,update_policy)
		VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11,$12,'disabled',$13,'',NOW(),NOW(),encode(sha256($7),'hex'),'bundled')
		ON CONFLICT(plugin_key) DO UPDATE SET name=EXCLUDED.name,version=EXCLUDED.version,description=EXCLUDED.description,author=EXCLUDED.author,manifest=EXCLUDED.manifest,
		artifact_data=EXCLUDED.artifact_data,artifact_path=EXCLUDED.artifact_path,install_path=EXCLUDED.install_path,binary_path=EXCLUDED.binary_path,binary_sha256=EXCLUDED.binary_sha256,
		signature_status=EXCLUDED.signature_status,state='disabled',package_sha256=EXCLUDED.package_sha256,update_policy='bundled',runtime_generation=sub2api_plugin_installations.runtime_generation+1,config_encrypted=CASE WHEN sub2api_plugin_installations.config_encrypted='' THEN EXCLUDED.config_encrypted ELSE sub2api_plugin_installations.config_encrypted END,last_error='',updated_at=NOW()
		RETURNING id`, plugin.PluginKey, plugin.Name, plugin.Version, plugin.Description, plugin.Author, manifest, plugin.ArtifactData, plugin.ArtifactPath, plugin.InstallPath, plugin.BinaryPath, plugin.BinarySHA256, plugin.SignatureStatus, config).Scan(&id)
	if err != nil {
		return nil, err
	}
	bindings := make([]service.PluginBinding, 0, len(plugin.Manifest.Capabilities))
	for _, capability := range plugin.Manifest.Capabilities {
		binding := service.PluginBinding{Capability: capability.ID, Platform: capability.Platform, AccountType: capability.AccountType, RolloutPercent: 100, Enabled: enabled && oldID == 0}
		for _, previous := range oldBindings {
			if previous.Capability == binding.Capability && previous.Platform == binding.Platform && previous.AccountType == binding.AccountType {
				binding.Enabled, binding.RolloutPercent = previous.Enabled, previous.RolloutPercent
				break
			}
		}
		bindings = append(bindings, binding)
	}
	bindingSnapshot, err = json.Marshal(bindings)
	if err != nil {
		return nil, err
	}
	stagedBindings := append([]service.PluginBinding(nil), bindings...)
	for index := range stagedBindings {
		stagedBindings[index].Enabled = false
	}
	if err = replacePluginBindings(ctx, tx, id, stagedBindings); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_bootstrap(plugin_key,bundle_sha256,migration_profile,desired_enabled,completed,state_imported,desired_bindings) VALUES($1,$2,$3,$4,false,$5,$6::jsonb)
		ON CONFLICT(plugin_key) DO UPDATE SET bundle_sha256=EXCLUDED.bundle_sha256,migration_profile=EXCLUDED.migration_profile,desired_enabled=EXCLUDED.desired_enabled,completed=false,state_imported=EXCLUDED.state_imported,desired_bindings=EXCLUDED.desired_bindings,updated_at=NOW()`, plugin.PluginKey, bundle, profile, enabled, imported, bindingSnapshot)
	if err != nil {
		return nil, err
	}
	if err = commitPluginRegistryTx(ctx, tx); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r *pluginRepository) CompleteBundledPlugin(ctx context.Context, id int64, bundle string) (resultErr error) {
	tx, err := r.beginPluginRegistryTx(ctx)
	if err != nil {
		return err
	}
	defer rollbackPluginRegistryTx(tx, &resultErr)
	var key string
	var enabled bool
	var completed, removed bool
	var currentState, policy string
	var bindingSnapshot []byte
	err = tx.QueryRowContext(ctx, `SELECT p.plugin_key,b.desired_enabled,b.desired_bindings,b.completed,b.user_removed,p.state,p.update_policy
		FROM sub2api_plugin_installations p JOIN sub2api_plugin_bootstrap b ON b.plugin_key=p.plugin_key
		WHERE p.id=$1 AND b.bundle_sha256=$2 FOR UPDATE OF p,b`, id, bundle).Scan(&key, &enabled, &bindingSnapshot, &completed, &removed, &currentState, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPluginStateChanged
	}
	if err != nil {
		return err
	}
	if removed || policy != service.PluginUpdateBundled || currentState == service.PluginStateUpdating {
		return service.ErrPluginStateChanged
	}
	// A delayed completion must not replay old binding intent after an operator
	// has disabled a completed bundle. Only an unfinished disabled generation is
	// still owned by bootstrap; dedicated updates own every updating generation.
	if completed {
		return nil
	}
	if currentState != service.PluginStateDisabled {
		return service.ErrPluginStateChanged
	}
	var bindings []service.PluginBinding
	if len(bindingSnapshot) == 0 || json.Unmarshal(bindingSnapshot, &bindings) != nil {
		return errors.New("plugin binding intent is unavailable")
	}
	if err = replacePluginBindings(ctx, tx, id, bindings); err != nil {
		return err
	}
	enabled = false
	for _, binding := range bindings {
		enabled = enabled || binding.Enabled
	}
	state := service.PluginStateDisabled
	if enabled {
		state = service.PluginStateEnabled
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_installations SET state=$2,updated_at=NOW() WHERE id=$1`, id, state); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sub2api_plugin_bootstrap SET completed=true,state_imported=true,updated_at=NOW() WHERE plugin_key=$1 AND bundle_sha256=$2`, key, bundle); err != nil {
		return err
	}
	return commitPluginRegistryTx(ctx, tx)
}

func (r *pluginRepository) LegacyBundleSeed(ctx context.Context, key, profile string, fallback service.PluginBundleSeed) (service.PluginBundleSeed, error) {
	var imported bool
	err := r.db.QueryRowContext(ctx, `SELECT state_imported FROM sub2api_plugin_bootstrap WHERE plugin_key=$1 AND migration_profile=$2`, key, profile).Scan(&imported)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fallback, err
	}
	if imported {
		return fallback, nil
	}
	if profile == "" || profile == "cindy-provider-v1" || profile == "image-tools-v1" || profile == "admin-observability-v1" {
		return fallback, nil
	}
	if profile != "codex-tickets-v1" {
		return fallback, errors.New("unsupported legacy plugin migration")
	}
	var config map[string]any
	if err := json.Unmarshal(fallback.Config, &config); err != nil {
		return fallback, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT key,value FROM settings WHERE key IN ('openai_codex_ticket_enabled','openai_codex_ticket_harvest_proxy_url')`)
	if err != nil {
		return fallback, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fallback, err
		}
		switch key {
		case "openai_codex_ticket_enabled":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return fallback, errors.New("invalid legacy ticket switch")
			}
			config["enabled"] = v
		case "openai_codex_ticket_harvest_proxy_url":
			config["proxy_url"] = value
		}
	}
	if err := rows.Err(); err != nil {
		return fallback, err
	}
	fallback.Config, err = json.Marshal(config)
	return fallback, err
}
