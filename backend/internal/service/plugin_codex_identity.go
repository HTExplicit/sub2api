package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func invokeCodexIdentityPolicy(ctx context.Context, operation string, query extensionv1.CodexIdentityQuery) (extensionv1.CodexIdentityResult, error) {
	raw, err := json.Marshal(query)
	if err != nil || len(raw) > 8192 {
		return extensionv1.CodexIdentityResult{}, ErrExtensionOperationUnavailable
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessDomainExtension(call, extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: operation, Payload: raw,
	}, true)
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
	available, err := codexIdentityPolicyAvailable(ctx, account.Type)
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
	result, err := invokeCodexIdentityPolicy(ctx, "codex.identity.plan", extensionv1.CodexIdentityQuery{
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
	return codexOutboundIdentity{userAgent: result.UserAgent, originator: codexTUIOriginator, version: canonical.version}, nil
}

func codexIdentityPolicyAvailable(ctx context.Context, accountType string) (bool, error) {
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformOpenAI, accountType, extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "codex.identity.available", Payload: []byte(`{}`),
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
