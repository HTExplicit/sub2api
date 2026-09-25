package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// System prompts are a small library of named prompts, one global switch and
// one site-wide default. Every account inherits the default unless its
// extra.system_prompt binding turns it off or selects another library prompt.
// The whole configuration is one JSON setting; request paths read an
// in-memory snapshot and the binding carried by the scheduled account, so
// sending a request never queries the database for prompts.

// SettingKeySystemPrompts stores SystemPromptConfig as JSON.
const SettingKeySystemPrompts = "system_prompts"

// AccountExtraSystemPromptKey stores an account's SystemPromptBinding.
// A missing or invalid value means inherit.
const AccountExtraSystemPromptKey = "system_prompt"

const (
	SystemPromptPositionPrepend = "prepend"
	SystemPromptPositionAppend  = "append"

	SystemPromptRoleAuto      = "auto"
	SystemPromptRoleSystem    = "system"
	SystemPromptRoleDeveloper = "developer"

	SystemPromptModeInherit = "inherit"
	SystemPromptModeOff     = "off"
	SystemPromptModeCustom  = "custom"

	SystemPromptMaxBodyBytes     = 64 << 10
	SystemPromptMaxPrompts       = 50
	SystemPromptMaxBindingBatch  = 1000
	systemPromptMaxNameRunes     = 100
	systemPromptRefreshInterval  = time.Minute
	systemPromptRetryInterval    = 5 * time.Second
	systemPromptStoreTimeout     = 5 * time.Second
	systemPromptWarnInterval     = time.Minute
	systemPromptGeneratedIDBytes = 6
)

var systemPromptIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var (
	ErrSystemPromptUnavailable = infraerrors.ServiceUnavailable("SYSTEM_PROMPT_UNAVAILABLE", "system prompt configuration is unavailable")
	ErrSystemPromptInUse       = infraerrors.Conflict("SYSTEM_PROMPT_IN_USE", "a removed prompt is still selected by accounts")
)

func systemPromptInvalid(format string, args ...any) error {
	return infraerrors.BadRequest("SYSTEM_PROMPT_INVALID", fmt.Sprintf(format, args...))
}

// SystemPrompt is one library entry. Position is relative to the request's
// system instructions; role auto follows each protocol's native carrier.
type SystemPrompt struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Body     string `json:"body"`
	Position string `json:"position"`
	Role     string `json:"role"`
}

// SystemPromptConfig is the complete stored configuration.
type SystemPromptConfig struct {
	Enabled         bool           `json:"enabled"`
	DefaultPromptID string         `json:"default_prompt_id"`
	Prompts         []SystemPrompt `json:"prompts"`
}

// SystemPromptBinding is stored in accounts.extra.system_prompt.
type SystemPromptBinding struct {
	Mode     string `json:"mode"`
	PromptID string `json:"prompt_id,omitempty"`
}

// SystemPromptBindingCount is one group of accounts sharing a binding.
type SystemPromptBindingCount struct {
	Mode     string
	PromptID string
	Accounts int64
}

// SystemPromptUsage summarizes how accounts are bound.
type SystemPromptUsage struct {
	Inherit int64            `json:"inherit"`
	Off     int64            `json:"off"`
	Custom  map[string]int64 `json:"custom"`
}

// SystemPromptState is the admin view of the configuration.
type SystemPromptState struct {
	SystemPromptConfig
	Usage SystemPromptUsage `json:"usage"`
}

// SystemPromptBindingStats counts account bindings for the admin page.
type SystemPromptBindingStats interface {
	CountSystemPromptBindings(ctx context.Context) ([]SystemPromptBindingCount, error)
}

type systemPromptSnapshot struct {
	config SystemPromptConfig
	byID   map[string]SystemPrompt
}

// SystemPromptService owns the configuration and its in-memory snapshot.
type SystemPromptService struct {
	settings SettingRepository
	accounts AccountRepository
	stats    SystemPromptBindingStats

	snapshot    atomic.Pointer[systemPromptSnapshot]
	nextRefresh atomic.Int64
	refreshing  atomic.Bool
	warnedAt    atomic.Int64
}

func NewSystemPromptService(settings SettingRepository, accounts AccountRepository, stats SystemPromptBindingStats) *SystemPromptService {
	return &SystemPromptService{settings: settings, accounts: accounts, stats: stats}
}

// ProvideSystemPromptService loads the configuration once at startup. A load
// failure never blocks startup: requests are forwarded unchanged until a
// background refresh succeeds.
func ProvideSystemPromptService(settings SettingRepository, accounts AccountRepository, stats SystemPromptBindingStats) *SystemPromptService {
	svc := NewSystemPromptService(settings, accounts, stats)
	ctx, cancel := context.WithTimeout(context.Background(), systemPromptStoreTimeout)
	defer cancel()
	if err := svc.Reload(ctx); err != nil {
		slog.Warn("system_prompt.load_failed", "error", err)
	}
	return svc
}

// Reload replaces the snapshot with the stored configuration.
func (s *SystemPromptService) Reload(ctx context.Context) error {
	config, err := s.load(ctx)
	if err != nil {
		s.nextRefresh.Store(time.Now().Add(systemPromptRetryInterval).UnixNano())
		return err
	}
	s.publish(config)
	return nil
}

func (s *SystemPromptService) publish(config SystemPromptConfig) {
	byID := make(map[string]SystemPrompt, len(config.Prompts))
	for _, prompt := range config.Prompts {
		byID[prompt.ID] = prompt
	}
	s.snapshot.Store(&systemPromptSnapshot{config: config, byID: byID})
	s.nextRefresh.Store(time.Now().Add(systemPromptRefreshInterval).UnixNano())
}

// read returns the stored configuration as saved, without validation.
func (s *SystemPromptService) read(ctx context.Context) (SystemPromptConfig, error) {
	if s == nil || s.settings == nil {
		return SystemPromptConfig{}, ErrSystemPromptUnavailable
	}
	dbCtx, cancel := context.WithTimeout(ctx, systemPromptStoreTimeout)
	defer cancel()
	raw, err := s.settings.GetValue(dbCtx, SettingKeySystemPrompts)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(raw) == "") {
		return SystemPromptConfig{Prompts: []SystemPrompt{}}, nil
	}
	if err != nil {
		return SystemPromptConfig{}, fmt.Errorf("read system prompt configuration: %w", err)
	}
	var config SystemPromptConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return SystemPromptConfig{}, fmt.Errorf("decode system prompt configuration: %w", err)
	}
	if config.Prompts == nil {
		config.Prompts = []SystemPrompt{}
	}
	return config, nil
}

// load returns the validated stored configuration.
func (s *SystemPromptService) load(ctx context.Context) (SystemPromptConfig, error) {
	config, err := s.read(ctx)
	if err != nil {
		return SystemPromptConfig{}, err
	}
	normalized, err := normalizeSystemPromptConfig(config)
	if err != nil {
		return SystemPromptConfig{}, fmt.Errorf("stored system prompt configuration is invalid: %w", err)
	}
	return normalized, nil
}

// current returns the snapshot and schedules a background refresh when it is
// stale, so configuration saved on another instance converges without adding
// work to the request.
func (s *SystemPromptService) current() *systemPromptSnapshot {
	if s == nil {
		return nil
	}
	if time.Now().UnixNano() >= s.nextRefresh.Load() && s.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.refreshing.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), systemPromptStoreTimeout)
			defer cancel()
			if err := s.Reload(ctx); err != nil {
				s.warn("system_prompt.refresh_failed", "error", err)
			}
		}()
	}
	return s.snapshot.Load()
}

func (s *SystemPromptService) warn(msg string, args ...any) {
	now := time.Now().UnixNano()
	last := s.warnedAt.Load()
	if now-last < int64(systemPromptWarnInterval) || !s.warnedAt.CompareAndSwap(last, now) {
		return
	}
	slog.Warn(msg, args...)
}

// resolve selects the prompt for one send. It is fail-open: without a usable
// snapshot or binding the request is forwarded unchanged.
func (s *SystemPromptService) resolve(account *Account) (SystemPrompt, bool) {
	if s == nil || account == nil {
		return SystemPrompt{}, false
	}
	snapshot := s.current()
	if snapshot == nil {
		s.warn("system_prompt.unavailable", "account_id", account.ID)
		return SystemPrompt{}, false
	}
	if !snapshot.config.Enabled {
		return SystemPrompt{}, false
	}
	binding := AccountSystemPromptBinding(account)
	id := snapshot.config.DefaultPromptID
	switch binding.Mode {
	case SystemPromptModeOff:
		return SystemPrompt{}, false
	case SystemPromptModeCustom:
		id = binding.PromptID
	}
	prompt, ok := snapshot.byID[id]
	return prompt, ok && id != ""
}

// AccountSystemPromptBinding reads the account's binding; anything missing or
// malformed is inherit.
func AccountSystemPromptBinding(account *Account) SystemPromptBinding {
	inherit := SystemPromptBinding{Mode: SystemPromptModeInherit}
	if account == nil || account.Extra == nil {
		return inherit
	}
	raw, ok := account.Extra[AccountExtraSystemPromptKey]
	if !ok || raw == nil {
		return inherit
	}
	var binding SystemPromptBinding
	switch value := raw.(type) {
	case SystemPromptBinding:
		binding = value
	case map[string]any:
		binding.Mode, _ = value["mode"].(string)
		binding.PromptID, _ = value["prompt_id"].(string)
	default:
		encoded, err := json.Marshal(value)
		if err != nil || json.Unmarshal(encoded, &binding) != nil {
			return inherit
		}
	}
	switch binding.Mode {
	case SystemPromptModeOff:
		return SystemPromptBinding{Mode: SystemPromptModeOff}
	case SystemPromptModeCustom:
		if strings.TrimSpace(binding.PromptID) != "" {
			return SystemPromptBinding{Mode: SystemPromptModeCustom, PromptID: strings.TrimSpace(binding.PromptID)}
		}
	}
	return inherit
}

// State returns the stored configuration and the current account usage. A
// stored value that no longer validates is still returned for editing; it is
// never published to requests.
func (s *SystemPromptService) State(ctx context.Context) (SystemPromptState, error) {
	config, err := s.read(ctx)
	if err != nil {
		return SystemPromptState{}, err
	}
	if normalized, invalid := normalizeSystemPromptConfig(config); invalid == nil {
		config = normalized
		s.publish(config)
	}
	usage, _, err := s.usage(ctx)
	if err != nil {
		return SystemPromptState{}, err
	}
	return SystemPromptState{SystemPromptConfig: config, Usage: usage}, nil
}

// Save validates and stores the whole configuration. A prompt still selected
// by custom accounts cannot be removed.
func (s *SystemPromptService) Save(ctx context.Context, config SystemPromptConfig) (SystemPromptState, error) {
	if s == nil || s.settings == nil {
		return SystemPromptState{}, ErrSystemPromptUnavailable
	}
	normalized, err := normalizeSystemPromptConfig(config)
	if err != nil {
		return SystemPromptState{}, err
	}
	usage, custom, err := s.usage(ctx)
	if err != nil {
		return SystemPromptState{}, err
	}
	kept := make(map[string]bool, len(normalized.Prompts))
	for _, prompt := range normalized.Prompts {
		kept[prompt.ID] = true
	}
	if previous, readErr := s.read(ctx); readErr == nil {
		for _, prompt := range previous.Prompts {
			if accounts := custom[prompt.ID]; !kept[prompt.ID] && accounts > 0 {
				return SystemPromptState{}, ErrSystemPromptInUse.WithMetadata(map[string]string{"prompt_id": prompt.ID, "accounts": fmt.Sprint(accounts)})
			}
		}
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return SystemPromptState{}, err
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), systemPromptStoreTimeout)
	defer cancel()
	if err := s.settings.Set(dbCtx, SettingKeySystemPrompts, string(raw)); err != nil {
		return SystemPromptState{}, err
	}
	s.publish(normalized)
	return SystemPromptState{SystemPromptConfig: normalized, Usage: usage}, nil
}

// SetBindings writes one binding to every listed account.
func (s *SystemPromptService) SetBindings(ctx context.Context, accountIDs []int64, binding SystemPromptBinding) (int64, error) {
	if s == nil || s.accounts == nil {
		return 0, ErrSystemPromptUnavailable
	}
	ids := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]bool, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			return 0, systemPromptInvalid("account ids must be positive")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > SystemPromptMaxBindingBatch {
		return 0, systemPromptInvalid("select between 1 and %d accounts", SystemPromptMaxBindingBatch)
	}
	value := SystemPromptBinding{Mode: binding.Mode}
	switch binding.Mode {
	case SystemPromptModeInherit, SystemPromptModeOff:
	case SystemPromptModeCustom:
		config, err := s.load(ctx)
		if err != nil {
			return 0, err
		}
		value.PromptID = strings.TrimSpace(binding.PromptID)
		found := false
		for _, prompt := range config.Prompts {
			found = found || prompt.ID == value.PromptID
		}
		if !found {
			return 0, systemPromptInvalid("prompt %q does not exist", value.PromptID)
		}
	default:
		return 0, systemPromptInvalid("mode must be inherit, off or custom")
	}
	return s.accounts.BulkUpdate(ctx, ids, AccountBulkUpdate{Extra: map[string]any{AccountExtraSystemPromptKey: value}})
}

func (s *SystemPromptService) usage(ctx context.Context) (SystemPromptUsage, map[string]int64, error) {
	usage := SystemPromptUsage{Custom: map[string]int64{}}
	if s.stats == nil {
		return usage, usage.Custom, nil
	}
	counts, err := s.stats.CountSystemPromptBindings(ctx)
	if err != nil {
		return SystemPromptUsage{}, nil, err
	}
	for _, count := range counts {
		switch count.Mode {
		case SystemPromptModeOff:
			usage.Off += count.Accounts
		case SystemPromptModeCustom:
			if id := strings.TrimSpace(count.PromptID); id != "" {
				usage.Custom[id] += count.Accounts
				continue
			}
			usage.Inherit += count.Accounts
		default:
			usage.Inherit += count.Accounts
		}
	}
	return usage, usage.Custom, nil
}

func normalizeSystemPromptConfig(config SystemPromptConfig) (SystemPromptConfig, error) {
	if len(config.Prompts) > SystemPromptMaxPrompts {
		return SystemPromptConfig{}, systemPromptInvalid("at most %d prompts are allowed", SystemPromptMaxPrompts)
	}
	out := SystemPromptConfig{Enabled: config.Enabled, DefaultPromptID: strings.TrimSpace(config.DefaultPromptID), Prompts: make([]SystemPrompt, 0, len(config.Prompts))}
	seen := make(map[string]bool, len(config.Prompts))
	for index, prompt := range config.Prompts {
		prompt.ID = strings.TrimSpace(prompt.ID)
		if prompt.ID == "" {
			prompt.ID = newSystemPromptID(seen)
		}
		if !systemPromptIDPattern.MatchString(prompt.ID) {
			return SystemPromptConfig{}, systemPromptInvalid("prompt %d has an invalid id", index+1)
		}
		if seen[prompt.ID] {
			return SystemPromptConfig{}, systemPromptInvalid("prompt id %q is duplicated", prompt.ID)
		}
		seen[prompt.ID] = true
		prompt.Name = strings.TrimSpace(prompt.Name)
		if prompt.Name == "" || utf8.RuneCountInString(prompt.Name) > systemPromptMaxNameRunes {
			return SystemPromptConfig{}, systemPromptInvalid("prompt %d needs a name of at most %d characters", index+1, systemPromptMaxNameRunes)
		}
		if !utf8.ValidString(prompt.Body) || strings.ContainsRune(prompt.Body, '\x00') || strings.TrimSpace(prompt.Body) == "" {
			return SystemPromptConfig{}, systemPromptInvalid("prompt %q needs non-empty UTF-8 text", prompt.Name)
		}
		if len(prompt.Body) > SystemPromptMaxBodyBytes {
			return SystemPromptConfig{}, systemPromptInvalid("prompt %q exceeds %d bytes", prompt.Name, SystemPromptMaxBodyBytes)
		}
		switch prompt.Position {
		case "":
			prompt.Position = SystemPromptPositionAppend
		case SystemPromptPositionPrepend, SystemPromptPositionAppend:
		default:
			return SystemPromptConfig{}, systemPromptInvalid("prompt %q position must be prepend or append", prompt.Name)
		}
		switch prompt.Role {
		case "":
			prompt.Role = SystemPromptRoleAuto
		case SystemPromptRoleAuto, SystemPromptRoleSystem, SystemPromptRoleDeveloper:
		default:
			return SystemPromptConfig{}, systemPromptInvalid("prompt %q role must be auto, system or developer", prompt.Name)
		}
		out.Prompts = append(out.Prompts, prompt)
	}
	if out.DefaultPromptID != "" && !seen[out.DefaultPromptID] {
		return SystemPromptConfig{}, systemPromptInvalid("default prompt %q does not exist", out.DefaultPromptID)
	}
	return out, nil
}

func newSystemPromptID(taken map[string]bool) string {
	for {
		var raw [systemPromptGeneratedIDBytes]byte
		if _, err := rand.Read(raw[:]); err != nil {
			panic(fmt.Sprintf("system prompt id: %v", err))
		}
		id := "p-" + hex.EncodeToString(raw[:])
		if !taken[id] {
			return id
		}
	}
}
