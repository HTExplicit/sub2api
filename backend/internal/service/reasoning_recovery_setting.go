package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Reasoning recovery has one switch for every OpenAI account. It is one JSON
// setting; the forwarding path reads an in-memory value, so deciding whether a
// rejected reasoning ciphertext may be recovered never queries the database.

// SettingKeyReasoningRecoveryConfig stores ReasoningRecoveryConfig as JSON.
// Without a stored value recovery is enabled.
const SettingKeyReasoningRecoveryConfig = "reasoning_recovery_config"

const (
	reasoningRecoveryRefreshInterval = time.Minute
	reasoningRecoveryRetryInterval   = 5 * time.Second
	reasoningRecoveryStoreTimeout    = 5 * time.Second
	reasoningRecoveryWarnInterval    = time.Minute
)

var ErrReasoningRecoveryUnavailable = infraerrors.ServiceUnavailable("REASONING_RECOVERY_UNAVAILABLE", "reasoning recovery configuration is unavailable")

// ReasoningRecoveryConfig is the stored switch and its admin view.
type ReasoningRecoveryConfig struct {
	Enabled bool `json:"enabled"`
}

// DecodeReasoningRecoveryConfig accepts exactly {"enabled": <boolean>}.
func DecodeReasoningRecoveryConfig(raw []byte) (ReasoningRecoveryConfig, error) {
	var fields struct {
		Enabled *bool `json:"enabled"`
	}
	if err := DecodeSwitchSettings(raw, &fields, "enabled"); err != nil {
		return ReasoningRecoveryConfig{}, err
	}
	if fields.Enabled == nil {
		return ReasoningRecoveryConfig{}, errors.New("enabled is required")
	}
	return ReasoningRecoveryConfig{Enabled: *fields.Enabled}, nil
}

// ReasoningRecoveryService owns the switch and its in-memory value.
type ReasoningRecoveryService struct {
	settings SettingRepository

	// storeMu orders stored reads and writes, so a refresh that began before a
	// save cannot publish the older value after it.
	storeMu sync.Mutex
	// The zero value is the default: enabled.
	disabled    atomic.Bool
	nextRefresh atomic.Int64
	refreshing  atomic.Bool
	warnedAt    atomic.Int64
}

func NewReasoningRecoveryService(settings SettingRepository) *ReasoningRecoveryService {
	return &ReasoningRecoveryService{settings: settings}
}

// ProvideReasoningRecoveryService loads the switch once at startup. A failed
// read stops startup: a stored "off" must not silently become the enabled
// default.
func ProvideReasoningRecoveryService(settings SettingRepository) (*ReasoningRecoveryService, error) {
	svc := NewReasoningRecoveryService(settings)
	ctx, cancel := context.WithTimeout(context.Background(), reasoningRecoveryStoreTimeout)
	defer cancel()
	if _, err := svc.Get(ctx); err != nil {
		return nil, err
	}
	return svc, nil
}

// Enabled reports the switch for one forwarding decision. It reads memory only
// and never waits: when the value is stale a background refresh picks up what
// another instance saved. Without a service the default applies.
func (s *ReasoningRecoveryService) Enabled() bool {
	if s == nil {
		return true
	}
	if s.settings != nil && time.Now().UnixNano() >= s.nextRefresh.Load() && s.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.refreshing.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), reasoningRecoveryStoreTimeout)
			defer cancel()
			if _, err := s.Get(ctx); err != nil {
				s.warn("reasoning_recovery.refresh_failed", "error", err)
			}
		}()
	}
	return !s.disabled.Load()
}

// Get returns the stored switch and makes it this instance's value. A failed
// read keeps the value this instance already applies.
func (s *ReasoningRecoveryService) Get(ctx context.Context) (ReasoningRecoveryConfig, error) {
	if s == nil || s.settings == nil {
		return ReasoningRecoveryConfig{}, ErrReasoningRecoveryUnavailable
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	config, err := s.read(ctx)
	if err != nil {
		s.nextRefresh.Store(time.Now().Add(reasoningRecoveryRetryInterval).UnixNano())
		return ReasoningRecoveryConfig{}, err
	}
	s.publish(config)
	return config, nil
}

// Save stores the switch and applies it to this instance at once.
func (s *ReasoningRecoveryService) Save(ctx context.Context, config ReasoningRecoveryConfig) (ReasoningRecoveryConfig, error) {
	if s == nil || s.settings == nil {
		return ReasoningRecoveryConfig{}, ErrReasoningRecoveryUnavailable
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return ReasoningRecoveryConfig{}, err
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reasoningRecoveryStoreTimeout)
	defer cancel()
	if err := s.settings.Set(dbCtx, SettingKeyReasoningRecoveryConfig, string(raw)); err != nil {
		return ReasoningRecoveryConfig{}, err
	}
	s.publish(config)
	return config, nil
}

// read returns the stored switch; a missing row is the enabled default.
func (s *ReasoningRecoveryService) read(ctx context.Context) (ReasoningRecoveryConfig, error) {
	dbCtx, cancel := context.WithTimeout(ctx, reasoningRecoveryStoreTimeout)
	defer cancel()
	raw, err := s.settings.GetValue(dbCtx, SettingKeyReasoningRecoveryConfig)
	if errors.Is(err, ErrSettingNotFound) {
		return ReasoningRecoveryConfig{Enabled: true}, nil
	}
	if err != nil {
		return ReasoningRecoveryConfig{}, fmt.Errorf("read reasoning recovery configuration: %w", err)
	}
	config, err := DecodeReasoningRecoveryConfig([]byte(raw))
	if err != nil {
		return ReasoningRecoveryConfig{}, fmt.Errorf("stored reasoning recovery configuration is invalid: %w", err)
	}
	return config, nil
}

func (s *ReasoningRecoveryService) publish(config ReasoningRecoveryConfig) {
	s.disabled.Store(!config.Enabled)
	s.nextRefresh.Store(time.Now().Add(reasoningRecoveryRefreshInterval).UnixNano())
}

func (s *ReasoningRecoveryService) warn(msg string, args ...any) {
	now := time.Now().UnixNano()
	last := s.warnedAt.Load()
	if now-last < int64(reasoningRecoveryWarnInterval) || !s.warnedAt.CompareAndSwap(last, now) {
		return
	}
	slog.Warn(msg, args...)
}
