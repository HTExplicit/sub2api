package service

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type identityRefreshingOAuthClientStub struct {
	userAgent  string
	originator string
}

func (s *identityRefreshingOAuthClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	return nil, nil
}

func (s *identityRefreshingOAuthClientStub) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return &openai.TokenResponse{AccessToken: "legacy"}, nil
}

func (s *identityRefreshingOAuthClientStub) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return &openai.TokenResponse{AccessToken: "legacy"}, nil
}

func (s *identityRefreshingOAuthClientStub) RefreshTokenWithIdentity(_ context.Context, _, _, _, userAgent, originator string) (*openai.TokenResponse, error) {
	s.userAgent, s.originator = userAgent, originator
	return &openai.TokenResponse{AccessToken: "with-identity", ExpiresIn: 3600}, nil
}

// 已有账号刷新 token 时使用账号级 Codex 身份，而不是全局规范身份。
func TestRefreshAccountTokenUsesAccountCodexIdentity(t *testing.T) {
	stub := &identityRefreshingOAuthClientStub{}
	svc := NewOpenAIOAuthService(nil, stub)
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "rt", "chatgpt_account_id": "acct-9"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "with-identity", info.AccessToken)
	expected := resolveCodexOutboundIdentityForAccount(account, "")
	require.Equal(t, expected.userAgent, stub.userAgent)
	require.Equal(t, "codex-tui", stub.originator)
}

// 账号级身份：由种子确定性派生，UA 三处版本同源，形态与 codex-rs 的
// `codex-tui/<ver> (<os> <osver>; <arch>) <terminal> (codex-tui; <ver>)` 一致。
func TestEnforceCodexIdentityHeadersForAccountUsesDerivedTUIIdentity(t *testing.T) {
	seed := "6f2c1c7e-6e2d-4a4b-9f0b-2f5f0c8a1d33"
	account := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-5"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: seed}}

	first, ok := account.CodexClientIdentity()
	require.True(t, ok)
	second, ok := account.CodexClientIdentity()
	require.True(t, ok)
	require.Equal(t, first, second, "同一种子必须派生同一身份")

	h := http.Header{}
	h.Set("originator", "codex_cli_rs")
	h.Set("user-agent", "codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown")
	enforceCodexIdentityHeadersForAccount(h, account, "")

	version := resolveCodexOutboundIdentity("").version
	uaPattern := regexp.MustCompile(`^codex-tui/` + regexp.QuoteMeta(version) +
		` \((Mac OS|Windows|Ubuntu) [0-9]+\.[0-9]+\.[0-9]+; (arm64|x86_64)\) \S+ \(codex-tui; ` + regexp.QuoteMeta(version) + `\)$`)
	require.Regexp(t, uaPattern, h.Get("user-agent"))
	require.Equal(t, first.UserAgent(version), h.Get("user-agent"))
	require.Equal(t, "codex-tui", h.Get("originator"))
	require.Equal(t, version, h.Get("version"))
	require.Contains(t, []string{"seatbelt", "windows_sandbox", "seccomp"}, first.Sandbox)
}

// 身份是系统管理字段：表单带回的坏身份被忽略并按种子派生；schema 校验拒绝系统/沙箱不配对。
func TestCodexClientIdentityIsSystemManagedAndSchemaChecked(t *testing.T) {
	seed := "9d4b7a10-2c3e-4f5a-8b6c-1e2f3a4b5c6d"
	account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{codexFingerprintSeedExtraKey: seed}}
	crafted := map[string]any{"v": 1, "os_type": "Mac OS", "os_version": "15.5.0", "arch": "arm64",
		"terminal": "iTerm.app/3.5.10\r\nx-evil: 1", "sandbox": "seatbelt"}

	prepared := prepareCodexFingerprintExtraForUpdate(account, map[string]any{CodexClientIdentityExtraKey: crafted})
	got, ok := codexClientIdentityFromExtra(prepared)
	require.True(t, ok)
	require.Equal(t, deriveCodexClientIdentity(seed), got.withoutGeneratedAt())
	require.NotContains(t, sanitizedCodexFingerprintExtraUpdates(map[string]any{CodexClientIdentityExtraKey: crafted}), CodexClientIdentityExtraKey)

	mismatched := deriveCodexClientIdentity(seed)
	mismatched.Sandbox = "seccomp"
	mismatched.OSType = "Windows"
	require.False(t, mismatched.valid())
	require.Empty(t, codexClientIdentitySeed(&Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-4"}}), "没有系统种子就不派生身份")
}

func (id codexClientIdentity) withoutGeneratedAt() codexClientIdentity {
	id.GeneratedAt = ""
	return id
}

// turn metadata 的 sandbox 跟随最终出站 UA，而不是账号身份：管理员显式 UA 覆盖时二者可能不同。
func TestFingerprintSandboxFollowsEffectiveUserAgent(t *testing.T) {
	ids := &codexFingerprintIDs{sandbox: "seatbelt"}
	ids.alignSandboxWithUserAgent("codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown")
	require.Equal(t, "windows_sandbox", ids.sandbox)
	ids.alignSandboxWithUserAgent("codex-tui/0.150.0 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.150.0)")
	require.Equal(t, "seccomp", ids.sandbox)
	ids.alignSandboxWithUserAgent("codex-tui/0.150.0 (Mac OS 15.5.0; arm64) ghostty/1.3.1 (codex-tui; 0.150.0)")
	require.Equal(t, "seatbelt", ids.sandbox)
	ids.alignSandboxWithUserAgent("curl/8.0")
	require.Empty(t, ids.sandbox)
}

// 账号作用域改写保留 UUIDv7 的时间位，且同一原始 UUID 在任何字段都映射到同一个值。
func TestScopeCodexAccountIdentityValuePreservesUUIDv7TimestampAndEquality(t *testing.T) {
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-7"}}
	raw := uuid.Must(uuid.NewV7())

	scoped := scopeCodexAccountIdentityValue(account, 42, raw.String())
	parsed, err := uuid.Parse(scoped)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), parsed.Version())
	require.Equal(t, raw[:6], parsed[:6], "48 位时间戳必须保留")
	require.NotEqual(t, raw.String(), scoped)

	require.Equal(t, scoped, scopeCodexAccountIdentityValue(account, 42, raw.String()), "确定性")
	require.Equal(t, scoped+":0", scopeCodexAccountIdentityValue(account, 42, raw.String()+":0"), "window id 只改写 uuid 前缀")
	require.Equal(t, "compact:"+scoped, scopeCodexAccountIdentityValue(account, 42, "compact:"+raw.String()), "带来源前缀的 prompt_cache_key 只改写 uuid")
	require.NotEqual(t, scoped, scopeCodexAccountIdentityValue(account, 43, raw.String()), "不同 API Key 不同作用域")
}
