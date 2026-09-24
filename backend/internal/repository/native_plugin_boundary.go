package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var errRetiredNativePlugin = errors.New("this first-party plugin is now built into the host")

// The upstream manager sees only third-party installations. Native repositories
// and the retirement transaction deliberately read the original SQL directly.
type nativePluginBoundary struct {
	service.PluginRepository
	keys map[string]bool
	ids  map[int64]bool
}

func ProvidePluginRepository(db *sql.DB, bootstrap *service.NativeFeatureBootstrap) service.PluginRepository {
	return newNativePluginBoundary(NewPluginRepository(db), bootstrap)
}

func newNativePluginBoundary(base service.PluginRepository, bootstrap *service.NativeFeatureBootstrap) *nativePluginBoundary {
	r := &nativePluginBoundary{PluginRepository: base, keys: map[string]bool{}, ids: map[int64]bool{}}
	for _, key := range service.FirstPartyNativePluginKeys {
		r.keys[key] = true
	}
	if bootstrap != nil && bootstrap.Snapshot != nil && bootstrap.Snapshot.Completed {
		for key, plugin := range bootstrap.Snapshot.Plugins {
			if r.keys[key] && plugin.ID > 0 {
				r.ids[plugin.ID] = true
			}
		}
	}
	return r
}

func (r *nativePluginBoundary) List(ctx context.Context) ([]*service.PluginInstallation, error) {
	all, err := r.PluginRepository.List(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]*service.PluginInstallation, 0, len(all))
	for _, plugin := range all {
		if plugin != nil && !r.keys[plugin.PluginKey] && !r.ids[plugin.ID] {
			visible = append(visible, plugin)
		}
	}
	return visible, nil
}

func (r *nativePluginBoundary) GetByID(ctx context.Context, id int64) (*service.PluginInstallation, error) {
	if r.ids[id] {
		return nil, sql.ErrNoRows
	}
	plugin, err := r.PluginRepository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if plugin != nil && r.keys[plugin.PluginKey] {
		return nil, sql.ErrNoRows
	}
	return plugin, nil
}

func (r *nativePluginBoundary) GetByKey(ctx context.Context, key string) (*service.PluginInstallation, error) {
	if r.keys[key] {
		return nil, sql.ErrNoRows
	}
	return r.PluginRepository.GetByKey(ctx, key)
}

func (r *nativePluginBoundary) Install(ctx context.Context, plugin *service.PluginInstallation, bindings []service.PluginBinding) (*service.PluginInstallation, error) {
	if plugin == nil || r.keys[plugin.PluginKey] || r.ids[plugin.ID] {
		return nil, errRetiredNativePlugin
	}
	return r.PluginRepository.Install(ctx, plugin, bindings)
}

func (r *nativePluginBoundary) checkID(ctx context.Context, id int64) error {
	if r.ids[id] {
		return errRetiredNativePlugin
	}
	plugin, err := r.PluginRepository.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if plugin == nil || r.keys[plugin.PluginKey] {
		return errRetiredNativePlugin
	}
	return nil
}

func (r *nativePluginBoundary) GetArtifact(ctx context.Context, id int64) ([]byte, error) {
	if err := r.checkID(ctx, id); err != nil {
		return nil, err
	}
	return r.PluginRepository.GetArtifact(ctx, id)
}
func (r *nativePluginBoundary) Delete(ctx context.Context, id int64, digest string) error {
	if err := r.checkID(ctx, id); err != nil {
		return err
	}
	return r.PluginRepository.Delete(ctx, id, digest)
}
func (r *nativePluginBoundary) BeginEnable(ctx context.Context, id int64, digest, state string) error {
	if err := r.checkID(ctx, id); err != nil {
		return err
	}
	return r.PluginRepository.BeginEnable(ctx, id, digest, state)
}
func (r *nativePluginBoundary) MarkRuntimeHealthy(ctx context.Context, id int64, digest, cipher string) error {
	if err := r.checkID(ctx, id); err != nil {
		return err
	}
	return r.PluginRepository.MarkRuntimeHealthy(ctx, id, digest, cipher)
}
func (r *nativePluginBoundary) UpdateState(ctx context.Context, id int64, state, message string, enabled *time.Time, digest, expected string) error {
	if err := r.checkID(ctx, id); err != nil {
		return err
	}
	return r.PluginRepository.UpdateState(ctx, id, state, message, enabled, digest, expected)
}
func (r *nativePluginBoundary) UpdateConfig(ctx context.Context, id int64, cipher, digest string) error {
	if err := r.checkID(ctx, id); err != nil {
		return err
	}
	return r.PluginRepository.UpdateConfig(ctx, id, cipher, digest)
}
func (r *nativePluginBoundary) UpdateBindingsAndState(ctx context.Context, id int64, bindings []service.PluginBinding, state, message string, enabled *time.Time, expected, digest string) error {
	if err := r.checkID(ctx, id); err != nil {
		return err
	}
	return r.PluginRepository.UpdateBindingsAndState(ctx, id, bindings, state, message, enabled, expected, digest)
}
