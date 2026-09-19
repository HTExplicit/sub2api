package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"maps"
)

func withoutPluginAccountProjection(extra map[string]any) map[string]any {
	if _, exists := extra[service.PluginAccountProjectionKey]; !exists {
		return extra
	}
	copy := maps.Clone(extra)
	delete(copy, service.PluginAccountProjectionKey)
	return copy
}

func preservePluginAccountProjection(incoming, current map[string]any) map[string]any {
	extra := withoutPluginAccountProjection(incoming)
	if value, exists := current[service.PluginAccountProjectionKey]; exists {
		extra = maps.Clone(extra)
		if extra == nil {
			extra = make(map[string]any)
		}
		extra[service.PluginAccountProjectionKey] = value
	}
	return extra
}
