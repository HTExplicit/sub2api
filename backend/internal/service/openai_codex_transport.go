package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"

	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codextransport"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func codexTransportScope(account *Account) string {
	if account == nil {
		return "initial-authorization"
	}
	seed, _ := codexFingerprintSeed(account.Extra)
	installation := resolveConvergedInstallationID(account, seed)
	sum := sha256.Sum256([]byte(codexAccountIdentityNamespace(account) + "\x00" + installation))
	return fmt.Sprintf("%x", sum[:16])
}

func codexStrictClientError(err error) *UpstreamFailoverError {
	code := "codex_transport_profile_unavailable"
	if codextransport.IsFailure(err) {
		code = "codex_transport_failed"
	}
	return &UpstreamFailoverError{Scope: GatewayFailureScopeRequest, NextAccountAction: NextAccountStop,
		SuppressAccountHealthPenalty: true, ClientStatusCode: http.StatusBadGateway,
		ClientErrorType: "codex_transport_error", ClientErrorCode: code, ClientMessage: err.Error()}
}

// A successful handshake cannot make an interrupted, already-sent turn replayable.
func codexStrictResponseFailure(response *http.Response, err error) *UpstreamFailoverError {
	if response == nil || response.Request == nil {
		return nil
	}
	if _, ok := codextransport.FromContext(response.Request.Context()); !ok {
		return nil
	}
	return codexStrictClientError(&codextransport.Failure{Err: err})
}

func bindCodexTransport(request *http.Request, source *Account, policy *codexFingerprintPolicy, plane string) (*http.Request, error) {
	if request == nil || source == nil || !source.IsOpenAIOAuthLike() || !policy.enabled {
		return request, nil
	}
	if _, ok := codextransport.FromContext(request.Context()); ok {
		return request, nil
	}
	binding, err := codextransport.Select(request.Header.Get("User-Agent"), codexTransportScope(source), plane)
	if err != nil {
		return nil, codexStrictClientError(err)
	}
	binding.RequestZstd = codexRequestZstd.Load()
	return request.WithContext(codextransport.WithBinding(request.Context(), binding)), nil
}

func bindCodexAttemptRequest(c *gin.Context, request *http.Request, routed *Account) (*http.Request, error) {
	source := codexAccountIdentitySource(c, routed)
	policy := codexFingerprintPolicyForContext(c, routed)
	if source != nil && source.IsOpenAIOAuthLike() && policy.enabled && request.Header.Get("originator") == "" {
		identity := resolveCodexOutboundIdentityForAccount(source, codexAccountIdentityOverrideUA(source), policy)
		request.Header.Set("User-Agent", identity.userAgent)
	}
	return bindCodexTransport(request, source, policy, "http")
}

func codexTransportContext(ctx context.Context, headers http.Header, source *Account, policy *codexFingerprintPolicy, plane string) (context.Context, error) {
	if source == nil || !source.IsOpenAIOAuthLike() || !policy.enabled {
		return ctx, nil
	}
	binding, err := codextransport.Select(headers.Get("User-Agent"), codexTransportScope(source), plane)
	if err != nil {
		return nil, codexStrictClientError(err)
	}
	return codextransport.WithBinding(ctx, binding), nil
}

func codexWSBodyIdentityKey(body []byte) string {
	cm := gjson.GetBytes(body, "client_metadata")
	if !cm.IsObject() {
		return ""
	}
	var flat map[string]any
	if json.Unmarshal([]byte(cm.Raw), &flat) != nil {
		return ""
	}
	metadata, _, err := canonicalCodexMetadata(flat)
	if err != nil {
		return ""
	}
	values := []string{}
	for _, key := range []string{"installation_id", "session_id", "thread_id", "window_id"} {
		values = append(values, stringMetadataValue(metadata, key))
	}
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:16])
}

func CodexFingerprintSimulationEnabled() bool { return currentCodexFingerprintPolicy().enabled }
