package service

import (
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// These are read-only descriptions of the existing policy, not wire captures.
type CodexFingerprintAccountView struct {
	Mode              string                           `json:"mode"`
	Identity          codexIdentitySnapshot            `json:"identity"`
	SimulationEnabled bool                             `json:"simulation_enabled"`
	DeviceIdentity    *codexClientIdentity             `json:"device_identity"`
	EffectiveDevice   *CodexFingerprintEffectiveDevice `json:"effective_device"`
	IdentifierPolicy  CodexFingerprintIdentifierPolicy `json:"identifier_policy"`
}

type CodexFingerprintEffectiveDevice struct {
	OSType          *string `json:"os_type"`
	OSVersion       *string `json:"os_version"`
	Arch            *string `json:"arch"`
	Terminal        *string `json:"terminal"`
	PlatformSandbox *string `json:"platform_sandbox"`
}

type CodexFingerprintIdentifierRule struct {
	Behavior string  `json:"behavior"`
	Rule     string  `json:"rule"`
	Value    *string `json:"value"`
}

type CodexFingerprintIdentifierPolicy struct {
	InstallationID CodexFingerprintIdentifierRule `json:"installation_id"`
	SessionID      CodexFingerprintIdentifierRule `json:"session_id"`
	ThreadID       CodexFingerprintIdentifierRule `json:"thread_id"`
	ParentThreadID CodexFingerprintIdentifierRule `json:"parent_thread_id"`
	WindowID       CodexFingerprintIdentifierRule `json:"window_id"`
}

func DescribeCodexFingerprintAccount(routed, source *Account) CodexFingerprintAccountView {
	if source == nil {
		source = routed
	}
	policy := currentCodexFingerprintPolicy()
	view := CodexFingerprintAccountView{
		Mode:              string(routed.GetCodexFingerprintMode()),
		Identity:          resolveCodexIdentitySnapshot(routed, source, codexAccountIdentityOverrideUA(source), policy),
		SimulationEnabled: policy.enabled,
	}
	if device, ok := source.CodexClientIdentity(); ok {
		view.DeviceIdentity = &device
	}
	if policy.enabled {
		view.EffectiveDevice = describeCodexFingerprintEffectiveDevice(view.Identity.UserAgent)
	}
	view.IdentifierPolicy = describeCodexFingerprintIdentifierPolicy(source, view.Identity.FingerprintModeEffective)
	return view
}

var codexFingerprintDeviceUAPattern = regexp.MustCompile(`^.+?/\S+\s+\(([^;()]+);\s*([^()]+)\)(?:\s+(.*))?$`)
var codexFingerprintOSVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)

func codexFingerprintOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// Only parse fields actually present in the selected UA. A valid custom Codex
// UA need not carry device fields, so missing fields must remain unknown.
func describeCodexFingerprintEffectiveDevice(userAgent string) *CodexFingerprintEffectiveDevice {
	device := &CodexFingerprintEffectiveDevice{}
	parts := codexFingerprintDeviceUAPattern.FindStringSubmatch(userAgent)
	if len(parts) == 0 {
		return device
	}
	os := strings.TrimSpace(parts[1])
	if split := strings.LastIndexByte(os, ' '); split > 0 && codexFingerprintOSVersionPattern.MatchString(os[split+1:]) {
		device.OSVersion = codexFingerprintOptionalString(os[split+1:])
		os = os[:split]
	}
	device.OSType = codexFingerprintOptionalString(os)
	device.Arch = codexFingerprintOptionalString(parts[2])
	terminal := strings.TrimSpace(parts[3])
	if split := strings.LastIndex(terminal, " ("); split >= 0 && strings.HasSuffix(terminal, ")") {
		trailer := strings.TrimSuffix(terminal[split+2:], ")")
		if name, _, ok := strings.Cut(trailer, ";"); ok && openai.IsCodexOfficialClientOriginator(strings.TrimSpace(name)) {
			terminal = terminal[:split]
		}
	}
	device.Terminal = codexFingerprintOptionalString(terminal)
	device.PlatformSandbox = codexFingerprintOptionalString(codexSandboxForUserAgent(userAgent))
	return device
}

func describeCodexFingerprintIdentifierPolicy(source *Account, effectiveMode string) CodexFingerprintIdentifierPolicy {
	preserve := CodexFingerprintIdentifierRule{Behavior: "passthrough", Rule: "preserve_client"}
	result := CodexFingerprintIdentifierPolicy{preserve, preserve, preserve, preserve, preserve}
	mode := codexFingerprintMode(effectiveMode)
	if source == nil || mode == codexFingerprintOff {
		return result
	}
	seed, ok := codexFingerprintSeed(source.Extra)
	if !ok {
		return result
	}
	result.InstallationID = CodexFingerprintIdentifierRule{Behavior: "fixed", Rule: "account_device", Value: codexFingerprintOptionalString(resolveConvergedInstallationID(source, seed))}
	if mode == codexFingerprintDevice {
		return result
	}
	sessionID := resolveConvergedSessionID(seed)
	result.SessionID = CodexFingerprintIdentifierRule{Behavior: "fixed", Rule: "account_session", Value: &sessionID}
	result.ThreadID = CodexFingerprintIdentifierRule{Behavior: "request_derived", Rule: "mapped_thread"}
	result.ParentThreadID = CodexFingerprintIdentifierRule{Behavior: "request_derived", Rule: "mapped_parent_thread"}
	result.WindowID = CodexFingerprintIdentifierRule{Behavior: "request_derived", Rule: "mapped_window"}
	if mode == codexFingerprintFull {
		result.ThreadID = CodexFingerprintIdentifierRule{Behavior: "fixed", Rule: "account_thread", Value: &sessionID}
		result.ParentThreadID.Rule = "remove_parent_self_reference"
		result.WindowID.Rule = "merged_window"
	}
	return result
}
