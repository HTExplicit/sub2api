package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type nativeCodexRepository struct {
	db             *sql.DB
	refreshAccount func(context.Context, int64)
}

func NewNativeCodexRepository(db *sql.DB, accounts service.AccountRepository) service.NativeCodexRepository {
	r := &nativeCodexRepository{db: db}
	if concrete, ok := accounts.(*accountRepository); ok {
		r.refreshAccount = concrete.syncSchedulerAccountSnapshotDetached
	}
	return r
}

func nativeCodexRuntimeLockName(id int64) string {
	// Same lock as the retired plugin: old and native owners cannot overlap a cutover.
	return fmt.Sprintf("sub2api-plugin-runtime:%d", id)
}

func (r *nativeCodexRepository) ReadNativeCodexStoredConfig(ctx context.Context) (string, bool, error) {
	var cipher string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.NativeCodexSourceSettingKey).Scan(&cipher)
	if err == nil {
		return cipher, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT config_encrypted FROM sub2api_plugin_installations WHERE plugin_key=$1`, service.NativeCodexPluginKey).Scan(&cipher)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return cipher, cipher != "", err
}

type nativeCodexMetadataQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func nativeCodexRetired(raw string) bool {
	var record struct {
		Version   int                        `json:"version"`
		Completed bool                       `json:"completed"`
		Plugins   map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal([]byte(raw), &record) != nil || record.Version != 1 || !record.Completed {
		return false
	}
	plugin, ok := record.Plugins[service.NativeCodexPluginKey]
	return ok && len(plugin) > 0 && string(plugin) != "null"
}

func nativeCodexConfigRecord(raw string) (service.NativeCodexConfigRecord, error) {
	var record service.NativeCodexConfigRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.Version != 1 || record.ConfigVersion <= 0 || len(record.ConfigSHA256) != 64 {
		return record, service.ErrNativeCodexRuntimeChanged
	}
	if _, err := hex.DecodeString(record.ConfigSHA256); err != nil {
		return record, service.ErrNativeCodexRuntimeChanged
	}
	return record, nil
}

func readNativeCodexMetadata(ctx context.Context, q nativeCodexMetadataQuerier, lock string) (*service.NativeCodexMetadata, error) {
	metadata := &service.NativeCodexMetadata{}
	var state, retired, config string
	err := q.QueryRowContext(ctx, `SELECT id,runtime_generation,state FROM sub2api_plugin_installations WHERE plugin_key=$1`+lock, service.NativeCodexPluginKey).Scan(&metadata.ID, &metadata.RuntimeGeneration, &state)
	if err != nil {
		return nil, err
	}
	if state != "disabled" || metadata.ID <= 0 || metadata.RuntimeGeneration <= 0 {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	if err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.NativeCodexRetirementSettingKey).Scan(&retired); err != nil {
		return nil, err
	}
	if !nativeCodexRetired(retired) {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.NativeCodexConfigSettingKey).Scan(&config)
	if errors.Is(err, sql.ErrNoRows) {
		// A historical run can be read before the native config hash is first recorded.
		return metadata, nil
	}
	if err != nil {
		return nil, err
	}
	record, err := nativeCodexConfigRecord(config)
	if err != nil {
		return nil, err
	}
	metadata.ConfigVersion, metadata.ConfigSHA256 = record.ConfigVersion, record.ConfigSHA256
	return metadata, nil
}

func (r *nativeCodexRepository) LoadNativeCodexMetadata(ctx context.Context) (*service.NativeCodexMetadata, error) {
	return readNativeCodexMetadata(ctx, r.db, "")
}

func (r *nativeCodexRepository) ValidateLegacyCodexJobSource(ctx context.Context, pluginID int64) error {
	metadata, err := r.LoadNativeCodexMetadata(ctx)
	if err != nil {
		return err
	}
	if pluginID <= 0 || metadata.ID != pluginID {
		return service.ErrNativeCodexRuntimeChanged
	}
	var raw string
	if err = r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.NativeCodexRetirementSettingKey).Scan(&raw); err != nil {
		return err
	}
	var snapshot service.NativeRetirementSnapshot
	if json.Unmarshal([]byte(raw), &snapshot) != nil || snapshot.Version != 1 || !snapshot.Completed {
		return service.ErrNativeCodexRuntimeChanged
	}
	origin, exists := snapshot.Plugins[service.NativeCodexPluginKey]
	if !exists || origin.ID != pluginID || origin.NativeCreated {
		return service.ErrNativeCodexRuntimeChanged
	}
	return nil
}

func (r *nativeCodexRepository) SyncNativeCodexConfig(ctx context.Context, hash string) (*service.NativeCodexMetadata, error) {
	return r.syncNativeCodexConfig(ctx, hash, nil)
}
func (r *nativeCodexRepository) StoreNativeCodexConfig(ctx context.Context, cipher, hash string) (*service.NativeCodexMetadata, error) {
	if cipher == "" {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	return r.syncNativeCodexConfig(ctx, hash, &cipher)
}
func (r *nativeCodexRepository) syncNativeCodexConfig(ctx context.Context, hash string, cipher *string) (*service.NativeCodexMetadata, error) {
	if len(hash) != 64 {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	var id int64
	if err := r.db.QueryRowContext(ctx, `SELECT id FROM sub2api_plugin_installations WHERE plugin_key=$1`, service.NativeCodexPluginKey).Scan(&id); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock_shared(hashtextextended($1,0))`, nativeCodexRuntimeLockName(id)).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	metadata, err := readNativeCodexMetadata(ctx, tx, " FOR UPDATE")
	if err != nil {
		return nil, err
	}
	saveSource := func() error {
		if cipher == nil {
			return nil
		}
		_, saveErr := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, service.NativeCodexSourceSettingKey, *cipher)
		return saveErr
	}
	if metadata.ConfigSHA256 == hash {
		if err = saveSource(); err != nil {
			return nil, err
		}
		return metadata, tx.Commit()
	}
	// Same-policy saves need not disturb shared business leases. A real policy
	// change still requires the original exclusive cutover lock after drain.
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, nativeCodexRuntimeLockName(id)).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return nil, service.ErrNativeCodexRuntimeChanged
	}
	if metadata.ConfigVersion > 0 {
		if err = tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_installations SET runtime_generation=runtime_generation+1,updated_at=NOW() WHERE id=$1 AND plugin_key=$2 AND state='disabled' AND runtime_generation=$3 RETURNING runtime_generation`, metadata.ID, service.NativeCodexPluginKey, metadata.RuntimeGeneration).Scan(&metadata.RuntimeGeneration); err != nil {
			return nil, err
		}
	}
	// The first hash records the generation already advanced by the retirement transaction.
	metadata.ConfigVersion++
	metadata.ConfigSHA256 = hash
	raw, err := json.Marshal(service.NativeCodexConfigRecord{Version: 1, ConfigVersion: metadata.ConfigVersion, ConfigSHA256: hash})
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, service.NativeCodexConfigSettingKey, string(raw)); err != nil {
		return nil, err
	}
	if err = saveSource(); err != nil {
		return nil, err
	}
	return metadata, tx.Commit()
}

func lockNativeCodexExecution(ctx context.Context, tx *sql.Tx, key string) error {
	if key != service.NativeCodexPluginKey {
		return service.ErrNativeCodexRuntimeChanged
	}
	wanted, ok := service.NativeCodexExecutionFromContext(ctx)
	if !ok {
		return service.ErrNativeCodexRuntimeChanged
	}
	current, err := readNativeCodexMetadata(ctx, tx, " FOR SHARE")
	if err != nil {
		return err
	}
	if *current != wanted {
		return service.ErrNativeCodexRuntimeChanged
	}
	return nil
}
