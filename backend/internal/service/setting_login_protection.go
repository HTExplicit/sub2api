package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SettingKeyLoginProtection = "login_protection_settings"

type LoginProtectionSettings struct {
	Enabled       bool `json:"enabled"`
	WindowMinutes int  `json:"window_minutes"`
	MaxFailures   int  `json:"max_failures"`
	BanHours      int  `json:"ban_hours"`
}

func DefaultLoginProtectionSettings() LoginProtectionSettings {
	return LoginProtectionSettings{Enabled: true, WindowMinutes: 5, MaxFailures: 5, BanHours: 24}
}

func (s LoginProtectionSettings) Validate() error {
	if s.WindowMinutes < 1 || s.WindowMinutes > 1440 {
		return fmt.Errorf("window_minutes must be between 1 and 1440")
	}
	if s.MaxFailures < 1 || s.MaxFailures > 1000 {
		return fmt.Errorf("max_failures must be between 1 and 1000")
	}
	if s.BanHours < 1 || s.BanHours > 720 {
		return fmt.Errorf("ban_hours must be between 1 and 720")
	}
	return nil
}

type cachedLoginProtectionSettings struct {
	settings LoginProtectionSettings
	expires  time.Time
}

func (s *SettingService) GetLoginProtectionSettings(ctx context.Context) (LoginProtectionSettings, error) {
	settings := DefaultLoginProtectionSettings()
	value, err := s.settingRepo.GetValue(ctx, SettingKeyLoginProtection)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(value) == "") {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultLoginProtectionSettings(), fmt.Errorf("decode login protection settings: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return DefaultLoginProtectionSettings(), err
	}
	return settings, nil
}

func (s *SettingService) SetLoginProtectionSettings(ctx context.Context, settings LoginProtectionSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	s.loginProtectionMu.Lock()
	defer s.loginProtectionMu.Unlock()
	value, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyLoginProtection, string(value)); err != nil {
		return err
	}
	s.loginProtectionCache.Store(&cachedLoginProtectionSettings{settings, time.Now().Add(time.Minute)})
	return nil
}

func (s *SettingService) GetLoginProtectionSettingsCached(ctx context.Context) LoginProtectionSettings {
	if s == nil || s.settingRepo == nil {
		return DefaultLoginProtectionSettings()
	}
	if cached, ok := s.loginProtectionCache.Load().(*cachedLoginProtectionSettings); ok && time.Now().Before(cached.expires) {
		return cached.settings
	}
	value, _, _ := s.loginProtectionSF.Do(SettingKeyLoginProtection, func() (any, error) {
		s.loginProtectionMu.Lock()
		defer s.loginProtectionMu.Unlock()
		if cached, ok := s.loginProtectionCache.Load().(*cachedLoginProtectionSettings); ok && time.Now().Before(cached.expires) {
			return cached.settings, nil
		}
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		settings, err := s.GetLoginProtectionSettings(readCtx)
		ttl := time.Minute
		if err != nil {
			ttl = 5 * time.Second
			if prior, ok := s.loginProtectionCache.Load().(*cachedLoginProtectionSettings); ok {
				settings = prior.settings
			}
		}
		s.loginProtectionCache.Store(&cachedLoginProtectionSettings{settings, time.Now().Add(ttl)})
		return settings, nil
	})
	if settings, ok := value.(LoginProtectionSettings); ok {
		return settings
	}
	return DefaultLoginProtectionSettings()
}
