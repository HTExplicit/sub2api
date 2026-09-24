package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// readJSONSetting decodes a JSON-valued setting into target. It reports false
// when the setting is unset, unreadable or not valid JSON for target.
func (s *SettingService) readJSONSetting(ctx context.Context, key string, target any) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return false
	}
	return json.Unmarshal([]byte(raw), target) == nil
}

// writeJSONSetting stores value as a JSON-valued setting.
func (s *SettingService) writeJSONSetting(ctx context.Context, key string, value any) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("settings are unavailable")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	return s.settingRepo.Set(dbCtx, key, string(raw))
}
