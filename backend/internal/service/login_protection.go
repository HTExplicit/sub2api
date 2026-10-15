package service

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type LoginIPBan struct {
	IP        string    `json:"ip"`
	BlockedAt time.Time `json:"blocked_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type LoginProtectionStore interface {
	Check(context.Context, string) (*LoginIPBan, error)
	RecordFailure(context.Context, string, LoginProtectionSettings) (*LoginIPBan, bool, error)
	List(context.Context, int, int) ([]LoginIPBan, int64, error)
	Unban(context.Context, string) error
}

type LoginProtectionService struct {
	store    LoginProtectionStore
	settings *SettingService
	audit    *AuditLogService
	mu       sync.Mutex
	known    map[string]LoginIPBan
}

func NewLoginProtectionService(store LoginProtectionStore, settings *SettingService, audit *AuditLogService) *LoginProtectionService {
	return &LoginProtectionService{store: store, settings: settings, audit: audit, known: make(map[string]LoginIPBan)}
}

func NormalizeLoginProtectionIP(value string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || address.Zone() != "" || address.IsUnspecified() {
		return "", fmt.Errorf("invalid client IP")
	}
	return address.Unmap().String(), nil
}

func (s *LoginProtectionService) Settings(ctx context.Context) LoginProtectionSettings {
	return s.settings.GetLoginProtectionSettingsCached(ctx)
}

// Check returns a still-valid known ban alongside a storage error. Only login
// endpoints fail closed on that error; other traffic uses this positive fallback.
func (s *LoginProtectionService) Check(ctx context.Context, ip string) (*LoginIPBan, error) {
	if !s.Settings(ctx).Enabled {
		return nil, nil
	}
	ip, err := NormalizeLoginProtectionIP(ip)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	ban, err := s.store.Check(readCtx, ip)
	if err != nil {
		s.mu.Lock()
		known, ok := s.known[ip]
		if ok && !time.Now().Before(known.ExpiresAt) {
			delete(s.known, ip)
			ok = false
		}
		s.mu.Unlock()
		if ok {
			return &known, err
		}
		return nil, err
	}
	s.remember(ip, ban)
	return ban, nil
}

func (s *LoginProtectionService) RecordFailure(ctx context.Context, ip string) (*LoginIPBan, error) {
	settings := s.Settings(ctx)
	if !settings.Enabled {
		return nil, nil
	}
	ip, err := NormalizeLoginProtectionIP(ip)
	if err != nil {
		return nil, err
	}
	ban, created, err := s.store.RecordFailure(ctx, ip, settings)
	if err != nil {
		return nil, err
	}
	s.remember(ip, ban)
	if created && ban != nil && s.audit != nil {
		s.audit.Record(&AuditLog{Action: "auth.login_protection.ban", ClientIP: ip, StatusCode: 429,
			Extra: map[string]any{"blocked_at": ban.BlockedAt.Format(time.RFC3339Nano), "expires_at": ban.ExpiresAt.Format(time.RFC3339Nano),
				"window_minutes": settings.WindowMinutes, "max_failures": settings.MaxFailures, "ban_hours": settings.BanHours}})
	}
	return ban, nil
}

func (s *LoginProtectionService) List(ctx context.Context, page, size int) ([]LoginIPBan, int64, error) {
	return s.store.List(ctx, page, size)
}

func (s *LoginProtectionService) Unban(ctx context.Context, ip string) error {
	ip, err := NormalizeLoginProtectionIP(ip)
	if err != nil {
		return err
	}
	if err := s.store.Unban(ctx, ip); err != nil {
		return err
	}
	s.remember(ip, nil)
	return nil
}

func (s *LoginProtectionService) remember(ip string, ban *LoginIPBan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ban == nil {
		delete(s.known, ip)
		return
	}
	// Positive fallback only: never cache an allow decision, so a new shared ban
	// is visible to every instance on its very next request.
	if _, exists := s.known[ip]; !exists && len(s.known) >= 16384 {
		for key := range s.known {
			delete(s.known, key)
			break
		}
	}
	s.known[ip] = *ban
}
