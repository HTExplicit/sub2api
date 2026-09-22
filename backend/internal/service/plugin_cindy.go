package service

import (
	"context"
	"encoding/json"
	"github.com/tidwall/gjson"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func currentCindyProviderConfig() (extensionv1.CindyProviderConfig, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := invokeProcessExtensionCached(ctx, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.features", Payload: json.RawMessage(`{}`)})
	var config extensionv1.CindyProviderConfig
	if err != nil || out.Code != "" || json.Unmarshal(out.Payload, &config) != nil {
		return extensionv1.CindyProviderConfig{}, false
	}
	return config, true
}

func classifyCindyProviderResponse(account *Account, status int, body []byte) extensionv1.CindyResponseDecision {
	if account == nil {
		return extensionv1.CindyResponseDecision{}
	}
	text := func(path string) *string {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			return nil
		}
		copy := value.String()
		return &copy
	}
	observed := extensionv1.CindyObservedResponse{Status: status, ValidJSON: gjson.ValidBytes(body), EventType: text("type"), ErrorType: text("error.type"), ErrorCode: text("error.code"), ResponseErrorType: text("response.error.type"), ResponseErrorCode: text("response.error.code")}
	raw, _ := json.Marshal(observed)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(ctx, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.health", AccountID: account.ID, Payload: raw})
	var decision extensionv1.CindyResponseDecision
	if err != nil || result.Code != "" || json.Unmarshal(result.Payload, &decision) != nil {
		return extensionv1.CindyResponseDecision{}
	}
	return decision
}
