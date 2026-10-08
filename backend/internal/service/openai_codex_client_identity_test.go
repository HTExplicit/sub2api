package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
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

func TestCodexIdentitySnapshotIsSecretFreeAndConsistent(t *testing.T) {
	seed := "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-9"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: seed}}

	snapshot := resolveCodexIdentitySnapshot(account, account, "")
	require.Equal(t, "account", snapshot.IdentitySource)
	require.Equal(t, resolveCodexOutboundIdentityForAccount(account, "").userAgent, snapshot.UserAgent)
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(raw), seed)

	t.Cleanup(func() { SetCodexForceCLIEnabled(false) })
	account.Credentials["user_agent"] = "codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown"
	SetCodexForceCLIEnabled(true)
	forced := resolveCodexIdentitySnapshot(account, account, codexAccountIdentityOverrideUA(account))
	require.Equal(t, "account", forced.IdentitySource)
	require.Equal(t, deriveCodexClientIdentity(seed).UserAgent(forced.Version), forced.UserAgent,
		"ForceCodexCLI drops the custom UA while retaining the account's derived TUI profile")
}

func TestCodexIdentitySnapshotReportsTheSelectedFingerprintSource(t *testing.T) {
	SetCodexForceCLIEnabled(true)
	t.Cleanup(func() { SetCodexForceCLIEnabled(false) })
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}
	snapshot := resolveCodexIdentitySnapshot(account, account, "")
	require.Equal(t, "account", snapshot.IdentitySource)
	require.False(t, snapshot.UAOverridePresent)
	snapshot = resolveCodexIdentitySnapshot(account, account, "codex_cli_rs/0.144.0 (Windows 10.0.19045; x86_64) unknown")
	require.Equal(t, "override_ua", snapshot.IdentitySource)
	require.True(t, snapshot.UAOverridePresent)
	// An override that does not pair as a Codex identity leaves the canonical
	// identity in place; the flag is then the only sign that one is configured.
	snapshot = resolveCodexIdentitySnapshot(account, account, "explicit UA override")
	require.Equal(t, "canonical", snapshot.IdentitySource)
	require.True(t, snapshot.UAOverridePresent)
}

// 规范解析器给出的版本号不合法时，生效版本回退内置常量：账号仍以自己的身份出站，
// 请求头与 token 刷新带的都是这个合法版本，不合法的版本号进不了 User-Agent。
//
// 不得给本用例加 t.Parallel()：它改写进程级解析器。
func TestCodexAccountIdentityUsesBuiltInVersionWhenCanonicalVersionIsInvalid(t *testing.T) {
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "fixture-refresh"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"}}
	profile, ok := account.CodexClientIdentity()
	require.True(t, ok)
	expected := codexOutboundIdentity{
		userAgent:  profile.UserAgent(codexCLIVersion),
		originator: codexTUIOriginator,
		version:    codexCLIVersion,
	}

	for _, canonicalUA := range []string{
		"codex-tui/9.9.9_bad (Ubuntu 22.4.0; x86_64) xterm-256color",
		"codex-tui/0.150.0\r\nx: value (Ubuntu 22.4.0; x86_64) xterm-256color",
	} {
		SetCodexCanonicalUserAgentResolver(func() string { return canonicalUA })
		require.Equal(t, codexCLIVersion, resolveCodexOutboundIdentity("").version)
		require.Equal(t, expected, resolveCodexOutboundIdentityForAccount(account, ""))
		headers := http.Header{"Originator": []string{"fixture-original"}}
		enforceCodexIdentityHeadersForAccount(headers, account, "")
		require.Equal(t, expected.userAgent, headers.Get("User-Agent"))
		require.Equal(t, expected.originator, headers.Get("Originator"))
		require.Equal(t, expected.version, headers.Get("Version"))
		client := &identityRefreshingOAuthClientStub{}
		_, err := NewOpenAIOAuthService(nil, client).RefreshAccountToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, expected.userAgent, client.userAgent)
		require.Equal(t, expected.originator, client.originator)
	}
}

type codexIdentityBackfillRepo struct {
	AccountRepository
	accounts []Account
	written  map[int64]map[string]any
}

func (r *codexIdentityBackfillRepo) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return r.accounts, nil
}

func (r *codexIdentityBackfillRepo) UpdateExtraIfRevision(_ context.Context, id int64, _ time.Time, updates map[string]any) (bool, error) {
	r.written[id] = updates
	return true, nil
}

// 启动回填只给缺少合法身份的 OpenAI OAuth-like 账号写入按种子派生的身份。
func TestCodexIdentityBackfillWritesOnlyMissingIdentities(t *testing.T) {
	const seed = "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"
	stored := codexClientIdentityExtraValue(deriveCodexClientIdentity("6f2c1c7e-6e2d-4a4b-9f0b-2f5f0c8a1d33"), time.Unix(0, 0))
	repo := &codexIdentityBackfillRepo{written: map[int64]map[string]any{}, accounts: []Account{
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintSeedExtraKey: seed}},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintSeedExtraKey: seed, CodexClientIdentityExtraKey: stored}},
		{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}},
		{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: map[string]any{codexFingerprintSeedExtraKey: seed}},
		{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{codexFingerprintSeedExtraKey: seed}},
	}}

	updated, failed, err := NewCodexClientIdentityBackfillService(repo).RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, failed)
	require.Equal(t, 2, updated)
	require.Len(t, repo.written, 2)
	for _, id := range []int64{2, 7} {
		got, ok := codexClientIdentityFromExtra(repo.written[id])
		require.True(t, ok, "account %d", id)
		require.Equal(t, deriveCodexClientIdentity(seed), got.withoutGeneratedAt())
	}
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

// 同一种子派生同一身份，派生的每个分支都通过 schema 校验；UA 的两处版本与 sandbox 都和
// 身份自洽；持久化的身份带 CR/LF 时视为缺失，账号改用按种子派生的身份。
func TestCodexClientIdentityPreservesDeterminismAndProtocolPairing(t *testing.T) {
	const seed = "1c0a3d9e-58b2-4f8c-a2d1-7f3b9e6c4a55"
	first := deriveCodexClientIdentity(seed)
	require.Equal(t, first, deriveCodexClientIdentity(seed))
	require.True(t, first.valid())
	// 这 4096 个种子走到全部系统、系统版本、架构与终端分支。
	for i := 0; i < 4096; i++ {
		derived := deriveCodexClientIdentity(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		require.True(t, derived.valid(), "%+v", derived)
	}

	ua := first.UserAgent("0.150.0")
	require.True(t, strings.HasPrefix(ua, "codex-tui/0.150.0 "), ua)
	require.True(t, strings.HasSuffix(ua, "(codex-tui; 0.150.0)"), ua)
	require.Equal(t, first.Sandbox, codexSandboxForUserAgent(ua))

	bad := first
	bad.Terminal = "terminal\r\nx-header: value"
	require.False(t, bad.valid(), "header injection accepted")
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		codexFingerprintSeedExtraKey: seed,
		CodexClientIdentityExtraKey:  codexClientIdentityExtraValue(bad, time.Unix(0, 0)),
	}}
	got, ok := account.CodexClientIdentity()
	require.True(t, ok)
	require.Equal(t, first, got, "invalid stored identity used")
}

// 凭据面与推理面共用显式 UA 策略：ForceCodexCLI 开启时忽略账号自定义 UA，刷新 token 也用派生身份。
func TestRefreshAccountTokenSharesForceCodexCLIOverridePolicy(t *testing.T) {
	t.Cleanup(func() { SetCodexForceCLIEnabled(false) })
	customUA := "codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown"
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "rt", "chatgpt_account_id": "acct-12", "user_agent": customUA},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: "3b7f1c2d-9e8a-4b6c-8d5e-2f1a0b9c8d7e"}}

	SetCodexForceCLIEnabled(false)
	stub := &identityRefreshingOAuthClientStub{}
	_, err := NewOpenAIOAuthService(nil, stub).RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, resolveCodexOutboundIdentity(customUA).userAgent, stub.userAgent, "未强制时账号显式 UA 生效")

	SetCodexForceCLIEnabled(true)
	stub = &identityRefreshingOAuthClientStub{}
	_, err = NewOpenAIOAuthService(nil, stub).RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, resolveCodexOutboundIdentityForAccount(account, "").userAgent, stub.userAgent, "ForceCodexCLI 时与推理面一样忽略显式 UA")
}

// codexTestSeedForOS 返回一个派生出指定操作系统身份的合法种子（确定性搜索）。
func codexTestSeedForOS(t *testing.T, osType string) string {
	t.Helper()
	for i := 0; i < 4096; i++ {
		seed := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		if deriveCodexClientIdentity(seed).OSType == osType {
			return seed
		}
	}
	t.Fatalf("no seed derives %s", osType)
	return ""
}

// 影子账号：staging 的 sandbox 跟随构造器实际使用的父账号身份，而不是子账号自己的种子。
func TestStagedFingerprintSandboxFollowsParentIdentitySource(t *testing.T) {
	seedFor := func(osType string) string { return codexTestSeedForOS(t, osType) }
	child := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-21"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: seedFor(codexClientOSWindows)}}
	parent := &Account{ID: 20, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-20"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: seedFor(codexClientOSMac)}}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(codexAccountIdentitySourceContextKey, parent)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	ids := svc.resolveStagedCodexFingerprintIDs(c, child, c.Request.Header)
	require.NotNil(t, ids)
	require.Equal(t, codexClientSandboxMac, ids.sandbox, "sandbox 跟随父账号（最终 UA）的系统")
	require.Equal(t, codexClientSandboxWindows, svc.resolveStagedCodexFingerprintIDs(nil, child, nil).sandbox, "无父账号时跟随自身身份")
}

// 兼容 Messages 桥接删除 originator 后不做 ForAccount 收口：sandbox 跟随构造器实际写入的 UA
// （客户端 UA / 账号显式 UA / ForceCodexCLI 规范 UA），而不是账号 profile。
func TestEffectiveCodexOutboundUserAgentFollowsBridgeContract(t *testing.T) {
	account := &Account{ID: 23, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-23"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: codexTestSeedForOS(t, codexClientOSMac)}}
	clientHeaders := http.Header{}
	clientHeaders.Set("user-agent", "codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown")
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	require.Equal(t, codexClientSandboxMac, svc.resolveStagedCodexFingerprintIDs(c, account, clientHeaders).sandbox, "非桥接：跟随账号身份")
	setOpenAICompatMessagesBridgeContext(c, true)
	require.Equal(t, codexClientSandboxWindows, svc.resolveStagedCodexFingerprintIDs(c, account, clientHeaders).sandbox, "桥接：跟随构造器保留的客户端 UA")
	svc.cfg.Gateway.ForceCodexCLI = true
	require.Equal(t, codexSandboxForUserAgent(CodexCanonicalUserAgent()), svc.resolveStagedCodexFingerprintIDs(c, account, clientHeaders).sandbox, "桥接 + ForceCodexCLI：跟随规范 UA")
}

// 关闭整个模拟后不暂存收敛身份，保留客户端 UA 和实际 sandbox 元数据。
func TestStagedFingerprintSandboxFollowsPairedClientUAWhenEnforcementOff(t *testing.T) {
	t.Cleanup(func() { SetCodexIdentityEnforcementEnabled(true) })
	account := &Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "acct-22"},
		Extra:       map[string]any{codexFingerprintSeedExtraKey: codexTestSeedForOS(t, codexClientOSMac)}}
	clientHeaders := http.Header{}
	clientHeaders.Set("user-agent", "codex_cli_rs/0.150.0 (Windows 10.0.19045; x86_64) unknown")
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	SetCodexIdentityEnforcementEnabled(true)
	require.Equal(t, codexClientSandboxMac, svc.resolveStagedCodexFingerprintIDs(nil, account, clientHeaders).sandbox, "强制统一开启：跟随账号身份")
	SetCodexIdentityEnforcementEnabled(false)
	require.Nil(t, svc.resolveStagedCodexFingerprintIDs(nil, account, clientHeaders))
	require.Equal(t, clientHeaders.Get("user-agent"), svc.effectiveCodexOutboundUserAgent(nil, account, clientHeaders))
	clientHeaders.Set("user-agent", "curl/8.0")
	require.Nil(t, svc.resolveStagedCodexFingerprintIDs(nil, account, clientHeaders))
	require.Equal(t, "curl/8.0", svc.effectiveCodexOutboundUserAgent(nil, account, clientHeaders))
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
