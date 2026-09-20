package service

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// codexIdentitySnapshot is the secret-free execution state reported by an
// admin OAuth account test. The enforcement and ForceCodexCLI values are
// process state published by NewOpenAIGatewayService, so the snapshot describes
// the policy that was actually active in this process.
type codexIdentitySnapshot struct {
	SchemaVersion             int       `json:"v"`
	AccountID                 int64     `json:"account_id"`
	IdentityAccountID         int64     `json:"identity_account_id"`
	ShadowParentID            *int64    `json:"shadow_parent_id,omitempty"`
	AccountRevision           time.Time `json:"account_revision"`
	ProxyID                   *int64    `json:"proxy_id,omitempty"`
	Transport                 string    `json:"transport"`
	IdentitySource            string    `json:"identity_source"`
	IdentityPersisted         bool      `json:"identity_persisted"`
	EnforcementEnabled        bool      `json:"enforcement_enabled"`
	ForceCodexCLI             bool      `json:"force_codex_cli"`
	UserAgent                 string    `json:"user_agent"`
	Originator                string    `json:"originator"`
	Version                   string    `json:"version"`
	FingerprintModeConfigured string    `json:"fingerprint_mode_configured"`
	FingerprintModeEffective  string    `json:"fingerprint_mode_effective"`
	FingerprintReason         string    `json:"fingerprint_reason"`
	SessionIDPresent          bool      `json:"session_id_present"`
	ClientRequestIDPresent    bool      `json:"client_request_id_present"`
	ZstdEnabled               bool      `json:"zstd_enabled"`
	ZstdApplied               bool      `json:"zstd_applied"`
	ZstdReason                string    `json:"zstd_reason"`
}

// resolveCodexIdentitySnapshot computes the identity and fingerprint state
// from the routed account and the credential source used for this attempt.
// It intentionally contains no credential, seed, cookie, or proxy URL data.
func resolveCodexIdentitySnapshotContext(ctx context.Context, routed, source *Account, overrideUA string) codexIdentitySnapshot {
	if source == nil {
		source = routed
	}

	snapshot := codexIdentitySnapshot{
		SchemaVersion:      1,
		EnforcementEnabled: codexIdentityEnforcement.Load(),
		ForceCodexCLI:      codexForceCLI.Load(),
	}
	if source != nil && source.IsOpenAIOAuthLike() {
		if plan, err := codexTransportPlan(ctx, source.Type, extensionv1.CodexTransportQuery{}); err == nil {
			snapshot.ZstdEnabled = plan.Enabled
		}
	}
	if routed != nil {
		snapshot.AccountID = routed.ID
		snapshot.ShadowParentID = routed.ParentAccountID
		snapshot.ProxyID = routed.ProxyID

		if configured, ok := codexFingerprintModeExplicit(routed.Extra); ok {
			snapshot.FingerprintModeConfigured = string(configured)
		}
		snapshot.FingerprintModeEffective = string(routed.GetCodexFingerprintMode())
		if !routed.IsOpenAIOAuthLike() {
			snapshot.FingerprintModeEffective = string(codexFingerprintOff)
			snapshot.FingerprintReason = "non_oauth"
		} else if snapshot.FingerprintModeConfigured == string(codexFingerprintOff) {
			snapshot.FingerprintReason = "explicit_off"
		} else if codexFingerprintModeRequiresSeed(codexFingerprintMode(snapshot.FingerprintModeEffective)) {
			if _, ok := codexFingerprintSeed(routed.Extra); !ok {
				snapshot.FingerprintModeEffective = string(codexFingerprintOff)
				snapshot.FingerprintReason = "seed_missing"
			}
		}
		if snapshot.FingerprintReason == "" {
			snapshot.FingerprintReason = "ok"
		}
	}
	if source != nil {
		snapshot.IdentityAccountID = source.ID
		snapshot.AccountRevision = source.UpdatedAt
		snapshot.IdentityPersisted = func() bool {
			_, ok := codexClientIdentityFromExtra(source.Extra)
			return ok
		}()
	}

	identity := resolveCodexOutboundIdentityForAccount(source, overrideUA)
	snapshot.UserAgent = identity.userAgent
	snapshot.Originator = identity.originator
	snapshot.Version = identity.version
	snapshot.IdentitySource = "canonical"
	if strings.TrimSpace(overrideUA) != "" {
		// The selector rebuilds the effective version. Comparing complete UA
		// strings would mislabel a valid override carrying an older version.
		if _, _, ok := openai.PairCodexClientIdentity(overrideUA); ok {
			snapshot.IdentitySource = "override_ua"
		}
	} else if source != nil {
		if accountIdentity, ok := source.CodexClientIdentity(); ok && accountIdentity.UserAgent(identity.version) == identity.userAgent {
			snapshot.IdentitySource = "account"
		}
	}
	return snapshot
}

// withWire enriches a snapshot with values that only exist on the final wire
// request. Header values are limited to the three non-secret Codex identity
// headers and presence bits for the request correlation headers.
func (s codexIdentitySnapshot) withWire(transport string, hdr http.Header) codexIdentitySnapshot {
	s.Transport = transport
	if hdr == nil {
		hdr = http.Header{}
	}
	if ua := strings.TrimSpace(hdr.Get("User-Agent")); ua != "" {
		s.UserAgent = ua
		if originator, pairedUA, ok := openai.PairCodexClientIdentity(ua); ok {
			s.Originator = originator
			s.UserAgent = pairedUA
		}
	}
	if originator := strings.TrimSpace(hdr.Get("Originator")); originator != "" {
		s.Originator = originator
	}
	if version := strings.TrimSpace(hdr.Get("Version")); version != "" {
		s.Version = version
	}
	s.SessionIDPresent = strings.TrimSpace(hdr.Get("session-id")) != "" || strings.TrimSpace(hdr.Get("Session_ID")) != ""
	s.ClientRequestIDPresent = strings.TrimSpace(hdr.Get("x-client-request-id")) != ""

	if transport == "plugin" {
		s.ZstdApplied = false
		s.ZstdReason = "plugin_roundtrip"
		return s
	}
	encoding := strings.TrimSpace(hdr.Get("Content-Encoding"))
	s.ZstdApplied = strings.EqualFold(encoding, "zstd")
	switch {
	case s.ZstdApplied:
		s.ZstdReason = "applied"
	case !s.ZstdEnabled:
		s.ZstdReason = "disabled"
	case encoding != "":
		s.ZstdReason = "already_encoded"
	default:
		s.ZstdReason = "empty_body"
	}
	return s
}

// summary is deliberately short and ASCII-only for the existing status line
// in AccountTestModal. The full secret-free snapshot is carried in Data.
func (s codexIdentitySnapshot) summary() string {
	proxy := "none"
	if s.ProxyID != nil {
		proxy = strconv.FormatInt(*s.ProxyID, 10)
	}
	return fmt.Sprintf("codex identity: transport=%s source=%s originator=%s version=%s fingerprint=%s zstd=%s proxy=%s",
		s.Transport, s.IdentitySource, s.Originator, s.Version, s.FingerprintModeEffective, s.ZstdReason, proxy)
}
