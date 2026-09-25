package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	promptpolicy "github.com/Wei-Shaw/sub2api/internal/promptskills/policy"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	BusinessSystemPromptMaxBytes = 64 << 10

	BusinessSystemPromptProtocolResponses = "responses"
	BusinessSystemPromptProtocolChat      = "chat"
	BusinessSystemPromptProtocolMessages  = "messages"
	BusinessSystemPromptProtocolGemini    = "gemini"

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

// ApplyBusinessSystemPromptToJSON applies the bounded policy result to the
// original request locally. Large user histories never enter the policy input.
func ApplyBusinessSystemPromptToJSON(body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) ([]byte, BusinessSystemPromptApplication, error) {
	return ApplyBusinessSystemPromptToJSONContext(context.Background(), body, snapshot, target)
}

func ApplyBusinessSystemPromptToJSONContext(ctx context.Context, body []byte, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) ([]byte, BusinessSystemPromptApplication, error) {
	application, err := planBusinessSystemPrompt(ctx, snapshot, target)
	if err != nil {
		return nil, application, err
	}
	return applyBusinessSystemPromptApplication(body, application)
}

func planBusinessSystemPrompt(ctx context.Context, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) (BusinessSystemPromptApplication, error) {
	if err := ctx.Err(); err != nil {
		return BusinessSystemPromptApplication{}, fmt.Errorf("%w: %w", ErrBusinessSystemPromptUnavailable, err)
	}
	application, err := promptpolicy.PlanRules(snapshot, target)
	if err != nil {
		if errors.Is(err, promptpolicy.ErrPromptDeliveryUnsupported) {
			return application, fmt.Errorf("%w: %v", ErrPromptDeliveryUnsupported, err)
		}
		return application, fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, err)
	}
	return application, nil
}

// PlanBusinessSystemPrompt selects rules and their final protocol carriers
// without copying or modifying customer history. Anchor validation happens in
// the shared applier when the final outgoing sequence is available.
func PlanBusinessSystemPrompt(ctx context.Context, snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) (BusinessSystemPromptApplication, error) {
	return planBusinessSystemPrompt(ctx, snapshot, target)
}

func applyBusinessSystemPromptApplication(body []byte, application BusinessSystemPromptApplication) ([]byte, BusinessSystemPromptApplication, error) {
	if application.RulesPlan != nil {
		return applyPromptRules(body, application)
	}
	if !application.Applied {
		return body, application, nil
	}
	return nil, application, ErrBusinessSystemPromptUnavailable
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
