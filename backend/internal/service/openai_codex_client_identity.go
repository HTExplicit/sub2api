package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// CodexClientIdentityExtraKey 持久化在 accounts.extra 中的每账号 Codex 客户端身份。
// OpenAI OAuth（及 setup_token）账号一账号一身份：由系统种子确定性派生后固定，
// 所有出站请求（HTTP / 透传 / WS 握手 / 探针 / token 刷新）共用，使上游看到的
// 是同一台设备上运行的同一个真实 Codex TUI，而不是多个用户各自的客户端。
const CodexClientIdentityExtraKey = "codex_client_identity"

const codexClientIdentitySchemaVersion = 1 // legacy persisted representation remains valid

type codexClientIdentity extensionv1.CodexClientProfile

// Stable values used by the persisted protocol representation and diagnostics.
const (
	codexClientOSMac          = "Mac OS"
	codexClientOSWindows      = "Windows"
	codexClientOSUbuntu       = "Ubuntu"
	codexClientSandboxMac     = "seatbelt"
	codexClientSandboxWindows = "windows_sandbox"
	codexClientSandboxLinux   = "seccomp"
	codexTUIOriginator        = "codex-tui"
)

func (id codexClientIdentity) valid() bool {
	return id.validForAccountContext(context.Background(), nil)
}

func (id codexClientIdentity) validForAccountContext(ctx context.Context, account *Account) bool {
	result, err := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.validate", extensionv1.CodexIdentityQuery{Profile: extensionv1.CodexClientProfile(id)})
	return err == nil && result.Valid
}

func (id codexClientIdentity) UserAgent(version string) string {
	return id.userAgentForAccountContext(context.Background(), nil, version)
}

func (id codexClientIdentity) userAgentForAccountContext(ctx context.Context, account *Account, version string) string {
	result, _ := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.agent", extensionv1.CodexIdentityQuery{Profile: extensionv1.CodexClientProfile(id), Version: version})
	return result.UserAgent
}

func codexSandboxForUserAgent(userAgent string) string {
	return codexSandboxForUserAgentForAccountContext(context.Background(), nil, userAgent)
}

func codexSandboxForUserAgentForAccountContext(ctx context.Context, account *Account, userAgent string) string {
	result, _ := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.sandbox", extensionv1.CodexIdentityQuery{UserAgent: userAgent})
	return result.Sandbox
}

func deriveCodexClientIdentity(seed string) codexClientIdentity {
	return deriveCodexClientIdentityForAccountContext(context.Background(), nil, seed)
}

func deriveCodexClientIdentityForAccountContext(ctx context.Context, account *Account, seed string) codexClientIdentity {
	preset := "captured_windows_cli"
	if account != nil && account.ID > 0 {
		preset = "legacy"
	}
	result, _ := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.derive", extensionv1.CodexIdentityQuery{Seed: seed, Preset: preset})
	return codexClientIdentity(result.Profile)
}

// codexClientIdentityFromExtra 读取持久化身份；缺失、非对象或字段不全时返回 false。
func codexClientIdentityFromExtra(extra map[string]any) (codexClientIdentity, bool) {
	return codexClientIdentityFromExtraContext(context.Background(), nil, extra)
}

func codexClientIdentityFromExtraContext(ctx context.Context, account *Account, extra map[string]any) (codexClientIdentity, bool) {
	if extra == nil {
		return codexClientIdentity{}, false
	}
	raw, ok := extra[CodexClientIdentityExtraKey]
	if !ok || raw == nil {
		return codexClientIdentity{}, false
	}
	var encoded []byte
	switch typed := raw.(type) {
	case string:
		encoded = []byte(typed)
	case map[string]any:
		data, err := json.Marshal(typed)
		if err != nil {
			return codexClientIdentity{}, false
		}
		encoded = data
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return codexClientIdentity{}, false
		}
		encoded = data
	}
	var id codexClientIdentity
	if err := json.Unmarshal(encoded, &id); err != nil || !id.validForAccountContext(ctx, account) {
		return codexClientIdentity{}, false
	}
	return id, true
}

// codexClientIdentitySeed 返回派生身份用的稳定种子：只认系统管理的
// codex_fingerprint_seed（建/改账号与迁移 243 都保证 OAuth-like 账号有种子）。
// 没有种子时返回空串，账号退回全局规范身份，避免先按其他来源派生、补种子后身份漂移。
func codexClientIdentitySeed(account *Account) string {
	if account == nil {
		return ""
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		return seed
	}
	return ""
}

// CodexClientIdentity 返回账号的 Codex 客户端身份。仅 OpenAI OAuth-like 账号有身份：
// 优先读持久化值，缺失时按种子即时派生（派生确定性，落库前后结果一致）。
func (a *Account) CodexClientIdentity() (codexClientIdentity, bool) {
	return a.codexClientIdentityContext(context.Background())
}

func (a *Account) codexClientIdentityContext(ctx context.Context) (codexClientIdentity, bool) {
	if a == nil || !a.IsOpenAIOAuthLike() {
		return codexClientIdentity{}, false
	}
	if available, err := codexIdentityPolicyAvailable(ctx, a.Type, a.ID); err != nil || !available {
		return codexClientIdentity{}, false
	}
	if id, ok := codexClientIdentityFromExtraContext(ctx, a, a.Extra); ok {
		return id, true
	}
	seed := codexClientIdentitySeed(a)
	if seed == "" {
		return codexClientIdentity{}, false
	}
	identity := deriveCodexClientIdentityForAccountContext(ctx, a, seed)
	return identity, identity.Version == codexClientIdentitySchemaVersion || identity.Version == 2
}

// codexClientIdentityExtraValue 把身份编码为可直接写入 extra 的 map。
func codexClientIdentityExtraValue(id codexClientIdentity, now time.Time) map[string]any {
	return map[string]any{
		"source":         id.Source,
		"originator":     id.Originator,
		"client_version": id.ClientVersion,
		"v":              id.Version,
		"os_type":        id.OSType,
		"os_version":     id.OSVersion,
		"arch":           id.Arch,
		"terminal":       id.Terminal,
		"sandbox":        id.Sandbox,
		"generated_at":   now.UTC().Format(time.RFC3339),
	}
}

// ensureCodexClientIdentityExtra 在完整 extra 上补齐身份（已存在且合法则保持不变）。
// 只对 OpenAI OAuth-like 账号生效；种子取 extra 中的 codex_fingerprint_seed，
// 调用方需先确保种子已就位（prepareCodexFingerprintExtraFor* 负责）。
func ensureCodexClientIdentityExtra(platform, accountType string, extra map[string]any, now time.Time) map[string]any {
	// Creation has a known type but no persisted ID. Do not invent a rollout key.
	return ensureCodexClientIdentityExtraForAccount(&Account{Platform: platform, Type: accountType}, extra, now)
}

func ensureCodexClientIdentityExtraForAccount(account *Account, extra map[string]any, now time.Time) map[string]any {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return extra
	}
	if available, err := codexIdentityPolicyAvailable(context.Background(), account.Type, account.ID); err != nil || !available {
		return extra
	}
	if _, ok := codexClientIdentityFromExtraContext(context.Background(), account, extra); ok {
		return extra
	}
	seed, ok := codexFingerprintSeed(extra)
	if !ok {
		return extra
	}
	if extra == nil {
		extra = make(map[string]any, 1)
	}
	identity := deriveCodexClientIdentityForAccountContext(context.Background(), account, seed)
	if identity.Version != codexClientIdentitySchemaVersion && identity.Version != 2 {
		return extra
	}
	extra[CodexClientIdentityExtraKey] = codexClientIdentityExtraValue(identity, now)
	return extra
}

// CodexClientIdentityBackfillService 在启动时为存量 OpenAI OAuth-like 账号补齐并
// 持久化 Codex 客户端身份。一次性、幂等、尽力而为：失败只记日志，不影响启动。
type CodexClientIdentityBackfillService struct {
	accountRepo AccountRepository
	now         func() time.Time
	stopOnce    sync.Once
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

type accountExtraRevisionWriter interface {
	UpdateExtraIfRevision(context.Context, int64, time.Time, map[string]any) (bool, error)
}

func NewCodexClientIdentityBackfillService(accountRepo AccountRepository) *CodexClientIdentityBackfillService {
	return &CodexClientIdentityBackfillService{
		accountRepo: accountRepo,
		now:         time.Now,
		stopCh:      make(chan struct{}),
	}
}

// Start 异步执行一次回填。
func (s *CodexClientIdentityBackfillService) Start() {
	if s == nil || s.accountRepo == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		go func() {
			select {
			case <-s.stopCh:
				cancel()
			case <-ctx.Done():
			}
		}()
		updated, failed, err := s.RunOnce(ctx)
		if err != nil {
			slog.Warn("codex_client_identity_backfill_failed", "error", err, "updated", updated, "failed", failed)
			return
		}
		if updated > 0 || failed > 0 {
			slog.Info("codex_client_identity_backfill_done", "updated", updated, "failed", failed)
		}
	}()
}

// Stop 取消进行中的回填并等待退出。
func (s *CodexClientIdentityBackfillService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

// RunOnce 为所有缺少合法身份的 OpenAI OAuth-like 账号写入派生身份。
// 返回成功更新数与失败数；列表查询失败时返回错误。
func (s *CodexClientIdentityBackfillService) RunOnce(ctx context.Context) (int, int, error) {
	if s == nil || s.accountRepo == nil {
		return 0, 0, nil
	}
	writer, ok := s.accountRepo.(accountExtraRevisionWriter)
	if !ok {
		return 0, 0, fmt.Errorf("atomic account metadata writer unavailable")
	}
	// 任务内取全部状态的 OpenAI 账号：ListByPlatform 只返回 active，而停用/异常账号恢复后
	// 同样要以持久化身份出站；不改动全局共享的 active 过滤，也不触碰账号状态。
	accounts, err := s.accountRepo.ListAllWithFilters(ctx, PlatformOpenAI, "", "", "", 0, "")
	if err != nil {
		return 0, 0, fmt.Errorf("list openai accounts: %w", err)
	}
	updated, failed := 0, 0
	for i := range accounts {
		account := &accounts[i]
		if !account.IsOpenAIOAuthLike() {
			continue
		}
		if available, err := codexIdentityPolicyAvailable(ctx, account.Type, account.ID); err != nil {
			return updated, failed, err
		} else if !available {
			continue
		}
		// The background scan may skip inapplicable accounts, but its policy
		// lease and write must stay scoped to each actual applicable account.
		if err := func() error {
			bound, release, err := bindProcessExtensionContext(ctx, account.Platform, account.Type, extensionv1.Invocation{
				Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.derive", AccountID: account.ID,
			})
			if errors.Is(err, ErrExtensionOperationDisabled) {
				return nil
			}
			if err != nil {
				return err
			}
			defer release()
			if _, ok := codexClientIdentityFromExtraContext(bound, account, account.Extra); ok {
				return nil
			}
			seed := codexClientIdentitySeed(account)
			if seed == "" {
				return nil
			}
			if err := bound.Err(); err != nil {
				return err
			}
			result, err := invokeCodexIdentityPolicyForAccount(bound, account, "codex.identity.derive", extensionv1.CodexIdentityQuery{Seed: seed, Preset: "legacy"})
			if err != nil {
				return err
			}
			value := codexClientIdentityExtraValue(codexClientIdentity(result.Profile), s.now())
			applied, err := writer.UpdateExtraIfRevision(bound, account.ID, account.UpdatedAt, map[string]any{CodexClientIdentityExtraKey: value})
			if err != nil {
				failed++
				slog.Warn("codex_client_identity_backfill_account_failed", "account_id", account.ID, "error", err)
				return nil
			}
			if applied {
				updated++
			}
			return nil
		}(); err != nil {
			return updated, failed, err
		}
	}
	return updated, failed, nil
}
