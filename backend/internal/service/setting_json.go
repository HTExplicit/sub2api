package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Startup must distinguish a missing key from a failed read, so a stored false
// switch cannot silently become an enabled deployment default.
func (s *SettingService) readNativeSwitchSetting(ctx context.Context, key string, target any, allowed ...string) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return false, errors.New("native settings repository is unavailable")
	}
	dbCtx, cancel := context.WithTimeout(ctx, gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, key)
	if errors.Is(err, ErrSettingNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot read native setting %s", key)
	}
	if err := DecodeSwitchSettings([]byte(raw), target, allowed...); err != nil {
		return false, fmt.Errorf("invalid native setting %s", key)
	}
	return true, nil
}

// readJSONSetting decodes a JSON-valued setting into target. It reports false
// when the setting is unset, unreadable or not valid JSON for target; read
// failures other than an unset key are logged, since callers then fall back
// to defaults.
func (s *SettingService) readJSONSetting(ctx context.Context, key string, target any) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, key)
	if err != nil {
		if !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("json_setting_read_failed", "key", key, "error", err)
		}
		return false
	}
	if strings.TrimSpace(raw) == "" {
		return false
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		slog.Warn("json_setting_invalid", "key", key, "error", err)
		return false
	}
	return true
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

// DecodeSwitchSettings decodes a JSON object of boolean switches into target.
// Unknown keys and non-boolean values are rejected, as the former plugin
// configuration validators did; omitted switches keep target's values.
func DecodeSwitchSettings(raw []byte, target any, allowed ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("settings must be a JSON object")
	}
	for key, value := range fields {
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known || (string(value) != "true" && string(value) != "false") {
			return errors.New("unknown or invalid switch setting")
		}
	}
	return json.Unmarshal(raw, target)
}
