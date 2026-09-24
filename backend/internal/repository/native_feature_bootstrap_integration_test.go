//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeFeatureRetirementIsAtomicAndPreservesLedger(t *testing.T) {
	ctx := context.Background()
	var present int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM sub2api_plugin_installations WHERE plugin_key IN ('codexrip.codex-runtime','codexrip.image-tools')`).Scan(&present))
	require.Zero(t, present, "this fixture requires an isolated test database")
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key IN ('deplugin_retired_plugins','image_tools_config')`)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_state WHERE plugin_key='codexrip.codex-runtime' AND namespace='codex-routing-private' AND state_key='quality-run.native-retirement-fixture'`)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_bootstrap WHERE plugin_key='codexrip.image-tools'`)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_installations WHERE plugin_key IN ('codexrip.codex-runtime','codexrip.image-tools')`)
	})
	insert := func(key string, generation int64) int64 {
		t.Helper()
		var id int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_installations
			(plugin_key,name,version,manifest,artifact_path,install_path,binary_path,binary_sha256,state,config_encrypted,runtime_generation,artifact_data)
			VALUES($1,'retirement fixture','1.0.0','{}','','','','fixture','enabled','original encrypted config',$2,decode('010203','hex')) RETURNING id`, key, generation).Scan(&id))
		return id
	}
	imageID := insert("codexrip.image-tools", 3)
	codexID := insert(service.NativeCodexPluginKey, 9)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO sub2api_plugin_bindings(plugin_id,capability,platform,account_type,enabled,rollout_percent) VALUES($1,'extensions.request.v1','*','*',true,100)`, imageID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO sub2api_plugin_bootstrap(plugin_key,bundle_sha256,migration_profile,desired_enabled,completed,state_imported) VALUES('codexrip.image-tools','fixture','image-tools-v1',true,true,true)`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES('image_tools_config','{"studio_enabled":false,"responses_image_enabled":false}',NOW())`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO sub2api_plugin_state(plugin_key,namespace,state_key,value) VALUES('codexrip.codex-runtime','codex-routing-private','quality-run.native-retirement-fixture','{"status":"closed","used_sends":40,"attempts":[{"id":"kept"}],"scope":{"account_id":7}}')`)
	require.NoError(t, err)
	var ledgerBefore string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT value::text FROM sub2api_plugin_state WHERE plugin_key='codexrip.codex-runtime' AND namespace='codex-routing-private' AND state_key='quality-run.native-retirement-fixture'`).Scan(&ledgerBefore))
	repo := NewNativeFeatureBootstrapRepository(integrationDB)
	convert := func(plugin service.NativeRetirementPlugin) (map[string]json.RawMessage, error) {
		if plugin.Key == "codexrip.image-tools" {
			return map[string]json.RawMessage{service.SettingKeyImageToolsConfig: json.RawMessage(`{"studio_enabled":true,"responses_image_enabled":true}`)}, nil
		}
		return nil, nil
	}
	_, err = repo.RetireNativeFeatures(ctx, func(plugin service.NativeRetirementPlugin) (map[string]json.RawMessage, error) {
		if plugin.Key == service.NativeCodexPluginKey {
			return nil, errors.New("fixture conversion failure")
		}
		return convert(plugin)
	})
	require.Error(t, err)
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM sub2api_plugin_installations WHERE id=$1`, imageID).Scan(&state))
	require.Equal(t, "enabled", state, "an earlier disable must roll back with a later failed conversion")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key='deplugin_retired_plugins'`).Scan(&present))
	require.Zero(t, present)

	first, err := repo.RetireNativeFeatures(ctx, convert)
	require.NoError(t, err)
	require.True(t, first.Completed)
	require.Equal(t, "enabled", first.Plugins[service.NativeCodexPluginKey].State)
	var generation int64
	var cipher, artifact string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state,runtime_generation,config_encrypted,encode(artifact_data,'hex') FROM sub2api_plugin_installations WHERE id=$1`, codexID).Scan(&state, &generation, &cipher, &artifact))
	require.Equal(t, "disabled", state)
	require.EqualValues(t, 10, generation)
	require.Equal(t, "original encrypted config", cipher)
	require.Equal(t, "010203", artifact)
	var setting string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='image_tools_config'`).Scan(&setting))
	require.JSONEq(t, `{"studio_enabled":false,"responses_image_enabled":false}`, setting)
	var bindingEnabled, removed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT enabled FROM sub2api_plugin_bindings WHERE plugin_id=$1`, imageID).Scan(&bindingEnabled))
	require.False(t, bindingEnabled)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT user_removed FROM sub2api_plugin_bootstrap WHERE plugin_key='codexrip.image-tools'`).Scan(&removed))
	require.True(t, removed)
	second, err := repo.RetireNativeFeatures(ctx, func(service.NativeRetirementPlugin) (map[string]json.RawMessage, error) {
		t.Fatal("a completed retirement must not repeat decryption, import, or generation changes")
		return nil, nil
	})
	require.NoError(t, err)
	firstJSON, err := json.Marshal(first.Plugins)
	require.NoError(t, err)
	secondJSON, err := json.Marshal(second.Plugins)
	require.NoError(t, err)
	require.JSONEq(t, string(firstJSON), string(secondJSON))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT runtime_generation FROM sub2api_plugin_installations WHERE id=$1`, codexID).Scan(&generation))
	require.EqualValues(t, 10, generation)
	var ledgerAfter string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT value::text FROM sub2api_plugin_state WHERE plugin_key='codexrip.codex-runtime' AND namespace='codex-routing-private' AND state_key='quality-run.native-retirement-fixture'`).Scan(&ledgerAfter))
	require.Equal(t, ledgerBefore, ledgerAfter)
}
