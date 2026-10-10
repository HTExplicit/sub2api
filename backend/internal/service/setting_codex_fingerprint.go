package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

const SettingKeyCodexFingerprintEnabled = "codex_fingerprint_enabled"

type CodexFingerprintSettings struct {
	Enabled                bool   `json:"enabled"`
	UserAgent              string `json:"user_agent"`
	ClientVersion          string `json:"client_version"`
	VersionAutoSyncEnabled bool   `json:"version_auto_sync_enabled"`
}

type CodexFingerprintSettingsView struct {
	CodexFingerprintSettings
	SyncedVersion      string `json:"synced_version"`
	EffectiveVersion   string `json:"effective_version"`
	EffectiveUserAgent string `json:"effective_user_agent"`
}

// An immutable policy is shared by the body, headers and connection selection of
// one upstream attempt. Publishing new settings never mutates an active attempt.
type codexFingerprintPolicy struct {
	enabled   bool
	userAgent string
	revision  uint64
}

var publishedCodexFingerprintPolicy atomic.Pointer[codexFingerprintPolicy]

var codexFingerprintAccountEpochs sync.Map
var codexFingerprintAccountEpoch atomic.Uint64

// Account mode edits invalidate only that account's subsequent WS handshakes.
func notifyCodexFingerprintAccountChanged(accountID int64) {
	codexFingerprintAccountEpochs.Store(accountID, codexFingerprintAccountEpoch.Add(1))
	cancelBorrowPolicyObservers(accountID)
}

func currentCodexFingerprintPolicyForAccount(account *Account) *codexFingerprintPolicy {
	policy := currentCodexFingerprintPolicy()
	if account == nil {
		return policy
	}
	if value, ok := codexFingerprintAccountEpochs.Load(account.ID); ok {
		epoch, valid := value.(uint64)
		if !valid {
			return policy
		}
		copy := *policy
		copy.revision ^= epoch * 0x9e3779b97f4a7c15
		return &copy
	}
	return policy
}

func codexFingerprintPolicyRevision(enabled bool, userAgent string) uint64 {
	digest := sha256.Sum256([]byte(strconv.FormatBool(enabled) + "\x00" + userAgent))
	return binary.BigEndian.Uint64(digest[:8])
}

func currentCodexFingerprintPolicy() *codexFingerprintPolicy {
	if policy := publishedCodexFingerprintPolicy.Load(); policy != nil {
		return policy
	}
	// Keep standalone tools and existing test construction working without Wire.
	ua := codexCanonicalUserAgent()
	enabled := codexIdentityEnforcement.Load()
	return &codexFingerprintPolicy{enabled: enabled, userAgent: ua, revision: codexFingerprintPolicyRevision(enabled, ua)}
}

type stagedCodexFingerprintPolicy struct {
	accountID int64
	policy    *codexFingerprintPolicy
}

const codexFingerprintPolicyContextKey = "codex_fingerprint_policy"

func stageCodexFingerprintPolicy(c *gin.Context, account *Account) *codexFingerprintPolicy {
	policy := currentCodexFingerprintPolicyForAccount(account)
	if c != nil && account != nil {
		c.Set(codexFingerprintPolicyContextKey, stagedCodexFingerprintPolicy{accountID: account.ID, policy: policy})
	}
	return policy
}

func codexFingerprintPolicyForContext(c *gin.Context, account *Account) *codexFingerprintPolicy {
	if c != nil && account != nil {
		if value, ok := c.Get(codexFingerprintPolicyContextKey); ok {
			if staged, ok := value.(stagedCodexFingerprintPolicy); ok && staged.accountID == account.ID {
				return staged.policy
			}
		}
	}
	return stageCodexFingerprintPolicy(c, account)
}

func selectCodexFingerprintPolicy(policies []*codexFingerprintPolicy) *codexFingerprintPolicy {
	if len(policies) > 0 && policies[0] != nil {
		return policies[0]
	}
	return currentCodexFingerprintPolicy()
}

func DecodeCodexFingerprintSettings(raw []byte) (CodexFingerprintSettings, error) {
	var config CodexFingerprintSettings
	if err := decodeCodexFingerprintObject(raw, &config, "enabled", "user_agent", "client_version", "version_auto_sync_enabled"); err != nil {
		return config, err
	}
	config.UserAgent = strings.TrimSpace(config.UserAgent)
	config.ClientVersion = strings.TrimSpace(config.ClientVersion)
	if len(config.UserAgent) > 512 || strings.ContainsAny(config.UserAgent, "\r\n") {
		return config, errors.New("user_agent must be a valid single-line Codex User-Agent of at most 512 bytes")
	}
	if config.ClientVersion != "" && NormalizeCodexClientVersion(config.ClientVersion) == "" {
		return config, errors.New("client_version must be empty or a valid Codex version")
	}
	return config, nil
}

func decodeCodexFingerprintObject(raw []byte, target any, keys ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil || len(fields) != len(keys) {
		return errors.New("expected a JSON object with exactly the supported settings")
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s is required and must not be null", key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

var codexFingerprintSettingKeys = []string{
	SettingKeyCodexFingerprintEnabled, SettingKeyOpenAICodexUserAgent,
	SettingKeyOpenAICodexClientVersion, SettingKeyOpenAICodexVersionAutoSyncEnabled,
	SettingKeyOpenAICodexClientVersionSynced,
}

func (s *SettingService) codexFingerprintSettingsFromValues(values map[string]string) (CodexFingerprintSettingsView, error) {
	config := CodexFingerprintSettings{Enabled: s.cfg == nil || !s.cfg.Gateway.DisableCodexIdentityEnforcement, VersionAutoSyncEnabled: true}
	if raw := values[SettingKeyCodexFingerprintEnabled]; raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return CodexFingerprintSettingsView{}, fmt.Errorf("invalid %s", SettingKeyCodexFingerprintEnabled)
		}
		config.Enabled = value
	}
	config.UserAgent = values[SettingKeyOpenAICodexUserAgent]
	config.ClientVersion = values[SettingKeyOpenAICodexClientVersion]
	if raw := strings.TrimSpace(values[SettingKeyOpenAICodexVersionAutoSyncEnabled]); raw != "" {
		config.VersionAutoSyncEnabled = raw == "true"
	}
	synced := NormalizeCodexClientVersion(values[SettingKeyOpenAICodexClientVersionSynced])
	version := NormalizeCodexClientVersion(config.ClientVersion)
	if version == "" {
		version = synced
	}
	if version == "" || CompareVersions(version, codexUpstreamMinVersion) < 0 {
		version = codexCLIVersion
	}
	ua := buildCodexCLIUserAgent(version)
	if _, paired, ok := openai.PairCodexClientIdentity(config.UserAgent); ok {
		if rebuilt := openai.SetCodexUserAgentVersion(paired, version); rebuilt != "" {
			ua = rebuilt
		}
	}
	return CodexFingerprintSettingsView{CodexFingerprintSettings: config, SyncedVersion: synced, EffectiveVersion: version, EffectiveUserAgent: ua}, nil
}

func publishCodexFingerprintSettings(view CodexFingerprintSettingsView) {
	previousPolicy := currentCodexFingerprintPolicy()
	publishedCodexFingerprintPolicy.Store(&codexFingerprintPolicy{
		enabled: view.Enabled, userAgent: view.EffectiveUserAgent,
		revision: codexFingerprintPolicyRevision(view.Enabled, view.EffectiveUserAgent),
	})
	if currentCodexFingerprintPolicy().revision != previousPolicy.revision {
		cancelBorrowPolicyObservers(0)
	}
}

func (s *SettingService) GetCodexFingerprintSettings(ctx context.Context) (CodexFingerprintSettingsView, error) {
	values, err := s.settingRepo.GetMultiple(ctx, codexFingerprintSettingKeys)
	if err != nil {
		return CodexFingerprintSettingsView{}, err
	}
	return s.codexFingerprintSettingsFromValues(values)
}

func (s *SettingService) LoadCodexFingerprintSettings(ctx context.Context) error {
	s.codexFingerprintMu.Lock()
	defer s.codexFingerprintMu.Unlock()
	view, err := s.GetCodexFingerprintSettings(ctx)
	if err != nil {
		return err
	}
	publishCodexFingerprintSettings(view)
	return nil
}

func (s *SettingService) UpdateCodexFingerprintSettings(ctx context.Context, config CodexFingerprintSettings) (CodexFingerprintSettingsView, error) {
	s.codexFingerprintMu.Lock()
	defer s.codexFingerprintMu.Unlock()
	values, err := s.settingRepo.GetMultiple(ctx, codexFingerprintSettingKeys)
	if err != nil {
		return CodexFingerprintSettingsView{}, err
	}
	updates := map[string]string{
		SettingKeyCodexFingerprintEnabled:           strconv.FormatBool(config.Enabled),
		SettingKeyOpenAICodexUserAgent:              config.UserAgent,
		SettingKeyOpenAICodexClientVersion:          config.ClientVersion,
		SettingKeyOpenAICodexVersionAutoSyncEnabled: strconv.FormatBool(config.VersionAutoSyncEnabled),
	}
	for key, value := range updates {
		values[key] = value
	}
	view, err := s.codexFingerprintSettingsFromValues(values)
	if err != nil {
		return CodexFingerprintSettingsView{}, err
	}
	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return CodexFingerprintSettingsView{}, err
	}
	s.InvalidateOpenAICodexClientVersionCache()
	s.openAICodexUASF.Forget("openai_codex_user_agent")
	s.openAICodexUACache.Store((*cachedOpenAICodexUserAgent)(nil))
	publishCodexFingerprintSettings(view)
	return view, nil
}

func DecodeCodexFingerprintMode(raw []byte) (string, error) {
	var config struct {
		Mode string `json:"mode"`
	}
	if err := decodeCodexFingerprintObject(raw, &config, "mode"); err != nil {
		return "", err
	}
	if _, ok := codexFingerprintModeExplicit(map[string]any{codexFingerprintModeExtraKey: config.Mode}); !ok {
		return "", errors.New("mode must be off, device, session or full")
	}
	return strings.TrimSpace(config.Mode), nil
}
