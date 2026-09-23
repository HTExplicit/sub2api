package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Only shared/pre-create policy material may use the account-free domain path.
// An existing account keeps its host-selected type and rollout scope on every
// policy operation, including validation and metadata-only diagnostics.
func invokeCodexIdentityPolicyForAccount(ctx context.Context, account *Account, operation string, query extensionv1.CodexIdentityQuery) (extensionv1.CodexIdentityResult, error) {
	if account != nil && !account.IsOpenAIOAuthLike() {
		return extensionv1.CodexIdentityResult{}, ErrExtensionOperationDisabled
	}
	raw, err := json.Marshal(query)
	if err != nil || len(raw) > 8192 {
		return extensionv1.CodexIdentityResult{}, ErrExtensionOperationUnavailable
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	invocation := extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: operation, Payload: raw,
	}
	var result extensionv1.Result
	if account == nil {
		result, err = invokeProcessDomainExtension(call, invocation, true)
	} else {
		invocation.AccountID = account.ID
		result, err = invokeProcessExtensionCached(call, account.Platform, account.Type, invocation)
	}
	if err != nil {
		return extensionv1.CodexIdentityResult{}, err
	}
	var identity extensionv1.CodexIdentityResult
	if result.Code != "" || json.Unmarshal(result.Payload, &identity) != nil {
		return identity, ErrExtensionOperationUnavailable
	}
	return identity, nil
}

func requireCodexIdentityPolicy(ctx context.Context, account *Account) error {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return nil
	}
	_, err := resolveCodexOutboundIdentityForAccountContext(ctx, account, codexAccountIdentityOverrideUA(account))
	return err
}

func resolveCodexOutboundIdentityForAccountContext(ctx context.Context, account *Account, overrideUA string) (codexOutboundIdentity, error) {
	canonical := resolveCodexOutboundIdentity(overrideUA)
	if account == nil || !account.IsOpenAIOAuthLike() {
		return canonical, nil
	}
	available, err := codexIdentityPolicyAvailable(ctx, account.Type, account.ID)
	if err != nil {
		return codexOutboundIdentity{}, err
	}
	if !available || overrideUA != "" {
		return canonical, nil
	}
	var profile extensionv1.CodexClientProfile
	if raw, ok := account.Extra[CodexClientIdentityExtraKey].(string); ok {
		_ = json.Unmarshal([]byte(raw), &profile)
	} else if raw, err := json.Marshal(account.Extra[CodexClientIdentityExtraKey]); err == nil {
		_ = json.Unmarshal(raw, &profile)
	}
	result, err := invokeCodexIdentityPolicyForAccount(ctx, account, "codex.identity.plan", extensionv1.CodexIdentityQuery{
		Seed: codexClientIdentitySeed(account), Profile: profile, Version: canonical.version,
	})
	if err != nil {
		if errors.Is(err, ErrExtensionOperationDisabled) {
			err = ErrExtensionOperationUnavailable
		}
		return codexOutboundIdentity{}, err
	}
	if !result.Valid {
		return canonical, nil
	}
	if result.UserAgent == "" {
		return codexOutboundIdentity{}, ErrExtensionOperationUnavailable
	}
	originator, paired, valid := openai.PairCodexClientIdentity(result.UserAgent)
	if !valid {
		return codexOutboundIdentity{}, ErrExtensionOperationUnavailable
	}
	return codexOutboundIdentity{userAgent: paired, originator: originator, version: openai.CodexUserAgentVersion(paired)}, nil
}

func codexIdentityPolicyAvailable(ctx context.Context, accountType string, accountID int64) (bool, error) {
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformOpenAI, accountType, extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.available", Payload: []byte(`{}`), AccountID: accountID,
	})
	if errors.Is(err, ErrExtensionOperationDisabled) {
		return false, nil // Explicitly disabled: use the upstream host identity contract.
	}
	if err != nil {
		return false, err
	}
	var status extensionv1.CodexIdentityResult
	if result.Code != "" || json.Unmarshal(result.Payload, &status) != nil || !status.Valid {
		return false, ErrExtensionOperationUnavailable
	}
	return true, nil
}
