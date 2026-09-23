package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	BusinessSystemPromptMaxBytes = 64 << 10

	BusinessSystemPromptProtocolResponses = "responses"
	BusinessSystemPromptProtocolChat      = "chat"

	BusinessSystemPromptCarrierInstructions  = "instructions"
	BusinessSystemPromptCarrierSystemMessage = "system_message"
)

var (
	ErrBusinessSystemPromptInvalid     = errors.New("invalid business system prompt")
	ErrBusinessSystemPromptUnavailable = errors.New("business system prompt unavailable")
)

type BusinessSystemPromptSnapshot = extensionv1.BusinessSystemPromptSnapshot

type BusinessSystemPromptTarget = extensionv1.BusinessSystemPromptTarget

type BusinessSystemPromptApplication = extensionv1.BusinessSystemPromptApplication

func ValidateBusinessSystemPromptBody(body string) (string, int, error) {
	return validateBusinessSystemPromptBodyWithLimit(body, BusinessSystemPromptMaxBytes)
}

func validateBusinessSystemPromptBodyWithLimit(body string, maxBytes int) (string, int, error) {
	hash, length, err := extensionv1.ValidateTextDocument(body, maxBytes)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrBusinessSystemPromptInvalid, err)
	}
	return hash, length, nil
}

func MergeBusinessSystemPromptInstructions(client, server string) string {
	client = strings.TrimSpace(client)
	server = strings.TrimSpace(server)
	switch {
	case client == "":
		return server
	case server == "":
		return client
	default:
		return client + "\n\n" + server
	}
}

// ApplyBusinessSystemPromptToJSON applies the plugin's bounded policy result to
// the original request locally. Large user histories never cross the RPC limit.
func ApplyBusinessSystemPromptToJSON(body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) ([]byte, BusinessSystemPromptApplication, error) {
	return ApplyBusinessSystemPromptToJSONContext(context.Background(), body, snapshot, target)
}

func ApplyBusinessSystemPromptToJSONContext(ctx context.Context, body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) ([]byte, BusinessSystemPromptApplication, error) {
	return applyBusinessSystemPromptWithInvoker(ctx, body, snapshot, target, invokeProcessExtension)
}

func applyBusinessSystemPromptWithInvoker(parent context.Context, body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget, invoke func(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error)) ([]byte, BusinessSystemPromptApplication, error) {
	application, err := planBusinessSystemPromptWithInvoker(parent, body, snapshot, target, invoke)
	if err != nil {
		return nil, application, err
	}
	return applyBusinessSystemPromptApplication(body, application)
}

// Planning rechecks the live execution scope, including on request-cache hits.
// Only bounded policy material and a presence bit cross the RPC boundary.
func planBusinessSystemPromptWithInvoker(parent context.Context, body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget, invoke func(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error)) (BusinessSystemPromptApplication, error) {
	request := extensionv1.PromptPlanRequest{Snapshot: snapshot, Target: target, HasInstructions: gjson.GetBytes(body, "instructions").Exists(), BaseSHA256: snapshot.BaseSHA256, EffectiveSHA256: snapshot.EffectiveSHA256, EffectiveByteLength: snapshot.EffectiveByteLength}
	raw, err := json.Marshal(request)
	if err != nil {
		return BusinessSystemPromptApplication{}, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	accountType := target.AccountType
	if accountType == "" {
		accountType = "*"
	}
	result, err := invoke(ctx, target.Platform, accountType, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.plan", AccountID: target.AccountID, Payload: raw})
	if errors.Is(err, ErrExtensionOperationDisabled) {
		if snapshot.RulePolicy != nil {
			plan := &extensionv1.PromptRulesPlan{Placements: []extensionv1.PromptRulePlacement{}, Skipped: []extensionv1.PromptRuleDecision{}}
			for _, rule := range snapshot.RulePolicy.Rules {
				plan.Skipped = append(plan.Skipped, extensionv1.PromptRuleDecision{RuleID: rule.ID, Reason: "plugin_scope"})
			}
			return BusinessSystemPromptApplication{Revision: snapshot.Revision, RulesPlan: plan}, nil
		}
		return BusinessSystemPromptApplication{}, nil
	}
	if result.Code == "prompt_delivery_unsupported" {
		return BusinessSystemPromptApplication{}, ErrPromptDeliveryUnsupported
	}
	if err != nil || result.Code != "" {
		if !snapshot.Enabled {
			return BusinessSystemPromptApplication{}, nil
		}
		return BusinessSystemPromptApplication{}, ErrBusinessSystemPromptUnavailable
	}
	var application BusinessSystemPromptApplication
	if json.Unmarshal(result.Payload, &application) != nil {
		return application, ErrBusinessSystemPromptUnavailable
	}
	if snapshot.RulePolicy != nil && application.RulesPlan == nil {
		if !snapshot.Enabled {
			return BusinessSystemPromptApplication{}, nil
		}
		return BusinessSystemPromptApplication{}, ErrBusinessSystemPromptUnavailable
	}
	return application, nil
}

func applyBusinessSystemPromptApplication(body []byte, application BusinessSystemPromptApplication) ([]byte, BusinessSystemPromptApplication, error) {
	if application.RulesPlan != nil {
		return applyPromptRules(body, application)
	}
	if !application.Applied {
		return body, application, nil
	}
	if !json.Valid(body) {
		return nil, application, fmt.Errorf("apply business system prompt: invalid JSON")
	}
	switch application.Carrier {
	case BusinessSystemPromptCarrierInstructions:
		return applyBusinessSystemPromptInstructions(body, application.ServerInstructions, application)
	case BusinessSystemPromptCarrierSystemMessage:
		return applyBusinessSystemPromptChatMessages(body, application.ServerInstructions, application)
	default:
		return nil, application, ErrBusinessSystemPromptUnavailable
	}
}

func applyBusinessSystemPromptInstructions(
	body []byte,
	server string,
	application BusinessSystemPromptApplication,
) ([]byte, BusinessSystemPromptApplication, error) {
	instructions := gjson.GetBytes(body, "instructions")
	if instructions.Exists() && instructions.Type != gjson.String {
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("apply business system prompt: instructions must be a string")
	}
	application.Carrier = BusinessSystemPromptCarrierInstructions
	application.ClientInstructions = strings.TrimSpace(instructions.String())
	merged := MergeBusinessSystemPromptInstructions(application.ClientInstructions, server)
	updated, err := sjson.SetBytes(body, "instructions", merged)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("apply business system prompt instructions: %w", err)
	}
	return updated, application, nil
}

func applyBusinessSystemPromptChatMessages(
	body []byte,
	server string,
	application BusinessSystemPromptApplication,
) ([]byte, BusinessSystemPromptApplication, error) {
	var envelope struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("parse chat messages: %w", err)
	}
	if envelope.Messages == nil {
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("apply business system prompt: messages must be an array")
	}

	insertAt := 0
	for insertAt < len(envelope.Messages) {
		role := strings.ToLower(strings.TrimSpace(gjson.GetBytes(envelope.Messages[insertAt], "role").String()))
		if role != "system" && role != "developer" {
			break
		}
		insertAt++
	}
	serverMessage, err := json.Marshal(map[string]string{
		"role":    "system",
		"content": strings.TrimSpace(server),
	})
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, err
	}
	messages := make([]json.RawMessage, 0, len(envelope.Messages)+1)
	messages = append(messages, envelope.Messages[:insertAt]...)
	messages = append(messages, serverMessage)
	messages = append(messages, envelope.Messages[insertAt:]...)
	rawMessages, err := json.Marshal(messages)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("marshal chat messages: %w", err)
	}
	updated, err := sjson.SetRawBytes(body, "messages", rawMessages)
	if err != nil {
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("apply business system prompt chat messages: %w", err)
	}
	application.Carrier = BusinessSystemPromptCarrierSystemMessage
	return updated, application, nil
}

func RewriteBusinessSystemPromptResponseJSON(
	body []byte,
	application BusinessSystemPromptApplication,
	exposeServerPrompt bool,
) ([]byte, error) {
	if application.RulesPlan != nil {
		return rewritePromptRulesResponse(body, application, exposeServerPrompt)
	}
	if exposeServerPrompt ||
		application.PreserveInstructionsEcho ||
		!application.Applied ||
		application.Carrier != BusinessSystemPromptCarrierInstructions {
		return body, nil
	}
	if !json.Valid(body) {
		return body, nil
	}
	expected := MergeBusinessSystemPromptInstructions(application.ClientInstructions, application.ServerInstructions)
	if expected == "" {
		return body, nil
	}
	out := body
	for _, path := range []string{
		"instructions",
		"response.instructions",
		"error.instructions",
		"error.response.instructions",
	} {
		value := gjson.GetBytes(out, path)
		if !value.Exists() || value.Type != gjson.String || value.String() != expected {
			continue
		}
		var err error
		if application.ClientInstructions == "" {
			out, err = sjson.DeleteBytes(out, path)
		} else {
			out, err = sjson.SetBytes(out, path, application.ClientInstructions)
		}
		if err != nil {
			return nil, fmt.Errorf("rewrite business system prompt response: %w", err)
		}
	}
	return out, nil
}

func RewriteBusinessSystemPromptSSE(
	body []byte,
	application BusinessSystemPromptApplication,
	exposeServerPrompt bool,
) ([]byte, error) {
	if exposeServerPrompt ||
		application.PreserveInstructionsEcho ||
		!application.Applied ||
		(application.Carrier != BusinessSystemPromptCarrierInstructions && application.RulesPlan == nil) {
		return body, nil
	}
	lines := bytes.SplitAfter(body, []byte("\n"))
	var out bytes.Buffer
	out.Grow(len(body))
	for _, rawLine := range lines {
		line := rawLine
		ending := []byte(nil)
		if bytes.HasSuffix(line, []byte("\r\n")) {
			line = line[:len(line)-2]
			ending = []byte("\r\n")
		} else if bytes.HasSuffix(line, []byte("\n")) {
			line = line[:len(line)-1]
			ending = []byte("\n")
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			payloadStart := len("data:")
			for payloadStart < len(line) && (line[payloadStart] == ' ' || line[payloadStart] == '\t') {
				payloadStart++
			}
			payload := line[payloadStart:]
			if !bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) && json.Valid(payload) {
				rewritten, err := RewriteBusinessSystemPromptResponseJSON(payload, application, false)
				if err != nil {
					return nil, err
				}
				_, _ = out.Write(line[:payloadStart])
				_, _ = out.Write(rewritten)
				_, _ = out.Write(ending)
				continue
			}
		}
		_, _ = out.Write(line)
		_, _ = out.Write(ending)
	}
	return out.Bytes(), nil
}
