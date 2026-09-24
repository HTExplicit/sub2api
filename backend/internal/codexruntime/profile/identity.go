package profile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability != extensionv1.CapabilityRequest || len(in.Payload) > 8192 {
		return extensionv1.Result{}, errors.New("invalid Codex identity request")
	}
	var query extensionv1.CodexIdentityQuery
	if json.Unmarshal(in.Payload, &query) != nil {
		return extensionv1.Result{}, errors.New("invalid Codex identity query")
	}
	result := extensionv1.CodexIdentityResult{DefaultFingerprintMode: "device"}
	switch in.Operation {
	case "codex.identity.available":
		result.Valid = true
	case "codex.identity.derive":
		if query.Seed == "" || len(query.Seed) > 1024 {
			return extensionv1.Result{}, errors.New("invalid identity seed")
		}
		identity := capturedCodexClientIdentity()
		if query.Preset == "legacy" {
			identity = deriveCodexClientIdentity(query.Seed)
		}
		result.Profile, result.Valid = extensionv1.CodexClientProfile(identity), true
	case "codex.identity.validate":
		result.Valid = codexClientIdentity(query.Profile).valid()
	case "codex.identity.plan":
		identity := codexClientIdentity(query.Profile)
		if !identity.valid() && query.Seed != "" && len(query.Seed) <= 1024 {
			identity = deriveCodexClientIdentity(query.Seed)
		}
		if identity.valid() {
			if len(query.Version) > 64 || !regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.]+)?$`).MatchString(query.Version) {
				return extensionv1.Result{}, errors.New("invalid identity version")
			}
			result.Profile, result.Valid = extensionv1.CodexClientProfile(identity), true
			result.UserAgent, result.Sandbox = identity.UserAgent(query.Version), identity.Sandbox
		}
	case "codex.identity.agent":
		if !codexClientIdentity(query.Profile).valid() || len(query.Version) > 64 || !regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.]+)?$`).MatchString(query.Version) {
			return extensionv1.Result{}, errors.New("invalid identity agent")
		}
		result.UserAgent = codexClientIdentity(query.Profile).UserAgent(query.Version)
	case "codex.identity.sandbox":
		result.Sandbox = codexSandboxForUserAgent(query.UserAgent)
	default:
		return extensionv1.Result{}, errors.New("unknown Codex identity operation")
	}
	raw, err := json.Marshal(result)
	return extensionv1.Result{Payload: raw}, err
}

const codexClientIdentitySchemaVersion = 1

// codexClientIdentity 描述一台"运行 Codex TUI 的机器"：操作系统、系统版本、架构、
// 终端以及与该系统配套的 sandbox 标签。字符串形态严格对齐 codex-rs 的
// os_info / terminal-detection / sandbox_tags 输出，保证 UA 与 turn metadata 自洽。
type codexClientIdentity extensionv1.CodexClientProfile

const (
	codexClientOSMac     = "Mac OS"
	codexClientOSWindows = "Windows"
	codexClientOSUbuntu  = "Ubuntu"

	codexClientSandboxMac     = "seatbelt"
	codexClientSandboxWindows = "windows_sandbox"
	codexClientSandboxLinux   = "seccomp"
)

var (
	codexClientMacVersions     = []string{"15.5.0", "15.6.1", "15.7.0", "26.0.1", "26.1.0"}
	codexClientWindowsVersions = []string{"10.0.26100", "10.0.22631", "10.0.19045"}
	codexClientUbuntuVersions  = []string{"22.4.0", "24.4.0"}
	codexClientAppleTerminals  = []string{"453", "455", "456"}
)

var (
	codexClientOSVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	codexClientTerminalPattern  = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,64}$`)
)

// valid 做 schema 校验而不只是非空：系统/沙箱必须配对（seatbelt=Mac OS、
// windows_sandbox=Windows、seccomp=Ubuntu），版本/架构/终端形态受限且不含空白或
// CR/LF。持久化值或外部写入的坏值一律视为缺失并重新按种子派生，避免脏值进入 UA。
func (id codexClientIdentity) valid() bool {
	if id.Version == 2 {
		return id == capturedCodexClientIdentity() || (id.GeneratedAt != "" && func() bool { id.GeneratedAt = ""; return id == capturedCodexClientIdentity() }())
	}
	if id.Version != codexClientIdentitySchemaVersion {
		return false
	}
	switch id.OSType {
	case codexClientOSMac:
		if id.Sandbox != codexClientSandboxMac || (id.Arch != "arm64" && id.Arch != "x86_64") {
			return false
		}
	case codexClientOSWindows:
		if id.Sandbox != codexClientSandboxWindows || id.Arch != "x86_64" {
			return false
		}
	case codexClientOSUbuntu:
		if id.Sandbox != codexClientSandboxLinux || id.Arch != "x86_64" {
			return false
		}
	default:
		return false
	}
	return codexClientOSVersionPattern.MatchString(id.OSVersion) &&
		codexClientTerminalPattern.MatchString(id.Terminal)
}

// codexSandboxForUserAgent 从有效出站 UA 的 `(<os> <ver>; <arch>)` 段推出与之配套的
// sandbox 标签；无法解析时返回空串（调用方不改写 sandbox）。turn metadata 声明的沙箱
// 必须跟随最终发出的 UA，而不是账号身份本身：管理员显式 UA 覆盖时二者可能不同。
var codexUserAgentPlatformPattern = regexp.MustCompile(`\(([^();]+?) [^ ();]+; [^();]+\)`)

func codexSandboxForUserAgent(userAgent string) string {
	m := codexUserAgentPlatformPattern.FindStringSubmatch(userAgent)
	if m == nil {
		return ""
	}
	osType := strings.ToLower(strings.TrimSpace(m[1]))
	switch {
	case osType == "":
		return ""
	case strings.HasPrefix(osType, "mac") || strings.HasPrefix(osType, "darwin"):
		return codexClientSandboxMac
	case strings.HasPrefix(osType, "windows"):
		return codexClientSandboxWindows
	default:
		return codexClientSandboxLinux
	}
}

// UserAgent 拼出真实 Codex TUI 的 User-Agent：
// codex-tui/<ver> (<os> <osver>; <arch>) <terminal> (codex-tui; <ver>)。
// 首段版本、尾部括号组版本与 version 头必须同源（codex-rs 三处都取同一个
// CARGO_PKG_VERSION）。
func (id codexClientIdentity) UserAgent(version string) string {
	if id.Version == 2 {
		return "codex_exec/0.156.0 (Windows 10.0.26220; x86_64) dumb (codex_exec; 0.156.0)"
	}
	version = strings.TrimSpace(version)
	return fmt.Sprintf("%s/%s (%s %s; %s) %s (%s; %s)",
		codexTUIOriginator, version, id.OSType, id.OSVersion, id.Arch, id.Terminal, codexTUIOriginator, version)
}

// codexTUIOriginator 是交互式 Codex TUI 的 originator（app-server initialize 的
// clientInfo.name，codex-rs tui/src/lib.rs）。
const codexTUIOriginator = "codex-tui"

// This is a reference-derived application profile, not a claim that the
// gateway's Go transport was captured from the native client.
func capturedCodexClientIdentity() codexClientIdentity {
	return codexClientIdentity{Version: 2, Source: "reference_derived_windows_cli", Originator: "codex_exec", ClientVersion: "0.156.0", OSType: "Windows", OSVersion: "10.0.26220", Arch: "x86_64", Terminal: "dumb", Sandbox: "windows_sandbox"}
}

// deriveCodexClientIdentity 从种子确定性派生一份自洽身份。分布按真实 TUI 用户
// 的大致构成加权：macOS 60%（arm64 为主），Windows 25%，Ubuntu 15%；终端与
// 系统匹配，sandbox 标签与系统匹配。
func deriveCodexClientIdentity(seed string) codexClientIdentity {
	h := sha256.Sum256([]byte("sub2api:codex-client-identity:v1:" + seed))
	id := codexClientIdentity{Version: codexClientIdentitySchemaVersion}
	switch osPick := h[0]; {
	case osPick < 154: // 60%
		id.OSType = codexClientOSMac
		id.Sandbox = codexClientSandboxMac
		if h[1] < 230 { // 90%
			id.Arch = "arm64"
		} else {
			id.Arch = "x86_64"
		}
		id.OSVersion = codexClientMacVersions[int(h[2])%len(codexClientMacVersions)]
		switch term := h[3]; {
		case term < 102: // 40%
			id.Terminal = fmt.Sprintf("iTerm.app/3.5.%d", 10+int(h[4])%6)
		case term < 153: // 20%
			id.Terminal = "Apple_Terminal/" + codexClientAppleTerminals[int(h[4])%len(codexClientAppleTerminals)]
		case term < 191: // 15%
			id.Terminal = fmt.Sprintf("ghostty/1.3.%d", int(h[4])%3)
		case term < 230: // 15%
			id.Terminal = codexClientVSCodeTerminal(h[4], h[5])
		default: // 10%
			id.Terminal = fmt.Sprintf("WarpTerminal/v0.2026.%02d.%02d.08.12.stable_%02d",
				1+int(h[4])%9, 1+int(h[5])%28, 1+int(h[6])%3)
		}
	case osPick < 218: // 25%
		id.OSType = codexClientOSWindows
		id.Sandbox = codexClientSandboxWindows
		id.Arch = "x86_64"
		id.OSVersion = codexClientWindowsVersions[int(h[2])%len(codexClientWindowsVersions)]
		switch term := h[3]; {
		case term < 153: // 60%
			id.Terminal = "WindowsTerminal"
		case term < 217: // 25%
			id.Terminal = codexClientVSCodeTerminal(h[4], h[5])
		default: // 15%
			id.Terminal = "unknown"
		}
	default: // 15%
		id.OSType = codexClientOSUbuntu
		id.Sandbox = codexClientSandboxLinux
		id.Arch = "x86_64"
		id.OSVersion = codexClientUbuntuVersions[int(h[2])%len(codexClientUbuntuVersions)]
		switch int(h[3]) % 3 {
		case 0:
			id.Terminal = "gnome-terminal"
		case 1:
			id.Terminal = "xterm-256color"
		default:
			id.Terminal = codexClientVSCodeTerminal(h[4], h[5])
		}
	}
	return id
}

func codexClientVSCodeTerminal(major, minor byte) string {
	return fmt.Sprintf("vscode/1.%d.%d", 103+int(major)%3, int(minor)%3)
}
