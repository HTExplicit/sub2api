package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func codexTransportPlan(ctx context.Context, accountType string, accountID int64, query extensionv1.CodexTransportQuery) (extensionv1.CodexTransportPlan, error) {
	raw, err := json.Marshal(query)
	if err != nil || len(raw) > 16384 {
		return extensionv1.CodexTransportPlan{}, ErrExtensionOperationUnavailable
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(call, PlatformOpenAI, accountType, extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "codex.transport.plan", Payload: raw, AccountID: accountID,
	})
	if errors.Is(err, ErrExtensionOperationDisabled) {
		return extensionv1.CodexTransportPlan{}, nil
	}
	if err != nil {
		return extensionv1.CodexTransportPlan{}, err
	}
	var plan extensionv1.CodexTransportPlan
	if result.Code != "" || json.Unmarshal(result.Payload, &plan) != nil || (plan.Compress && !plan.Enabled) {
		return plan, ErrExtensionOperationUnavailable
	}
	return plan, nil
}

func prepareCodexTransport(req *http.Request, account *Account) (*http.Request, error) {
	if req == nil || req.URL == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return req, nil
	}
	if _, ok := req.Context().Value(codexIdentityBodyKey{}).(codexIdentityBodyObservation); !ok {
		req = req.WithContext(context.WithValue(req.Context(), codexIdentityBodyKey{}, inspectCodexIdentityBody(req)))
	}
	if err := requireCodexIdentityPolicy(req.Context(), account); err != nil {
		return nil, err
	}
	plan, err := codexTransportPlan(req.Context(), account.Type, account.ID, extensionv1.CodexTransportQuery{
		Method: req.Method, Path: req.URL.Path, ContentType: req.Header.Get("Content-Type"), ContentEncoding: req.Header.Get("Content-Encoding"),
		BodyPresent: req.Body != nil && req.Body != http.NoBody,
	})
	if err != nil {
		return nil, err
	}
	if !plan.Compress {
		return req, nil
	}
	return prepareOpenAICodexWireRequestUngated(req, account)
}
