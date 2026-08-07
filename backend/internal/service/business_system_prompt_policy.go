package service

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

//go:embed prompts/codexrip_reverse_skill_system_prompt.txt
var embeddedBusinessSystemPrompt string

type BusinessSystemPromptSnapshot struct {
	Enabled              bool      `json:"enabled"`
	ExposeServerPrompt   bool      `json:"expose_server_prompt"`
	CompactEnabled       bool      `json:"compact_enabled"`
	TemplateID           int64     `json:"template_id"`
	VersionID            int64     `json:"version_id"`
	TemplateVersion      int64     `json:"template_version"`
	Revision             int64     `json:"revision"`
	Body                 string    `json:"body,omitempty"`
	SHA256               string    `json:"sha256"`
	ByteLength           int       `json:"byte_length"`
	CompositionMode      string    `json:"composition_mode"`
	BundleID             string    `json:"bundle_id,omitempty"`
	BundleManifestSHA256 string    `json:"bundle_manifest_sha256,omitempty"`
	BundleAvailable      bool      `json:"bundle_available"`
	BundleDegraded       bool      `json:"bundle_degraded"`
	DegradedReason       string    `json:"degraded_reason,omitempty"`
	Degraded             bool      `json:"degraded"`
	UpdatedAt            time.Time `json:"updated_at"`

	// requestBundle and the effective fields are attached to an immutable
	// request-scoped copy. They are never persisted or returned by the runtime
	// API, which continues to expose the active version's original hash/length.
	requestBundle       *BusinessSystemPromptBundle `json:"-"`
	baseSHA256          string                      `json:"-"`
	effectiveSHA256     string                      `json:"-"`
	effectiveByteLength int                         `json:"-"`
	routeIDs            []string                    `json:"-"`
	documentIDs         []string                    `json:"-"`
	referenceIDs        []string                    `json:"-"`
}

type BusinessSystemPromptTarget struct {
	Platform string
	Protocol string
	Compact  bool
}

type BusinessSystemPromptApplication struct {
	Applied              bool     `json:"applied"`
	Carrier              string   `json:"carrier"`
	ClientInstructions   string   `json:"client_instructions"`
	ServerInstructions   string   `json:"server_instructions"`
	ExposeServerPrompt   bool     `json:"expose_server_prompt"`
	CompactEnabled       bool     `json:"compact_enabled"`
	TemplateID           int64    `json:"template_id"`
	VersionID            int64    `json:"version_id"`
	TemplateVersion      int64    `json:"template_version"`
	Revision             int64    `json:"revision"`
	SHA256               string   `json:"sha256"`
	BaseSHA256           string   `json:"base_sha256,omitempty"`
	EffectiveSHA256      string   `json:"effective_sha256,omitempty"`
	EffectiveByteLength  int      `json:"effective_byte_length,omitempty"`
	CompositionMode      string   `json:"composition_mode,omitempty"`
	BundleID             string   `json:"bundle_id,omitempty"`
	BundleManifestSHA256 string   `json:"bundle_manifest_sha256,omitempty"`
	RouteIDs             []string `json:"route_ids,omitempty"`
	DocumentIDs          []string `json:"document_ids,omitempty"`
	ReferenceIDs         []string `json:"reference_ids,omitempty"`
	Degraded             bool     `json:"degraded,omitempty"`
}

func ValidateBusinessSystemPromptBody(body string) (string, int, error) {
	return validateBusinessSystemPromptBodyWithLimit(body, BusinessSystemPromptMaxBytes)
}

func validateBusinessSystemPromptBodyWithLimit(body string, maxBytes int) (string, int, error) {
	if !utf8.ValidString(body) {
		return "", 0, fmt.Errorf("%w: body is not valid UTF-8", ErrBusinessSystemPromptInvalid)
	}
	if strings.ContainsRune(body, '\x00') {
		return "", 0, fmt.Errorf("%w: body contains NUL", ErrBusinessSystemPromptInvalid)
	}
	if strings.TrimSpace(body) == "" {
		return "", 0, fmt.Errorf("%w: body is empty", ErrBusinessSystemPromptInvalid)
	}
	byteLength := len([]byte(body))
	if byteLength > maxBytes {
		return "", 0, fmt.Errorf("%w: body exceeds %d bytes", ErrBusinessSystemPromptInvalid, maxBytes)
	}
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:]), byteLength, nil
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

func ApplyBusinessSystemPromptToJSON(
	body []byte,
	snapshot BusinessSystemPromptSnapshot,
	target BusinessSystemPromptTarget,
) ([]byte, BusinessSystemPromptApplication, error) {
	application := BusinessSystemPromptApplication{
		ExposeServerPrompt:   snapshot.ExposeServerPrompt,
		CompactEnabled:       snapshot.CompactEnabled,
		TemplateID:           snapshot.TemplateID,
		VersionID:            snapshot.VersionID,
		TemplateVersion:      snapshot.TemplateVersion,
		Revision:             snapshot.Revision,
		SHA256:               strings.ToLower(strings.TrimSpace(snapshot.SHA256)),
		BaseSHA256:           strings.ToLower(strings.TrimSpace(snapshot.baseSHA256)),
		EffectiveSHA256:      strings.ToLower(strings.TrimSpace(snapshot.effectiveSHA256)),
		EffectiveByteLength:  snapshot.effectiveByteLength,
		CompositionMode:      snapshot.CompositionMode,
		BundleID:             snapshot.BundleID,
		BundleManifestSHA256: strings.ToLower(strings.TrimSpace(snapshot.BundleManifestSHA256)),
		RouteIDs:             append([]string(nil), snapshot.routeIDs...),
		DocumentIDs:          append([]string(nil), snapshot.documentIDs...),
		ReferenceIDs:         append([]string(nil), snapshot.referenceIDs...),
		Degraded:             snapshot.Degraded,
	}
	if !snapshot.Enabled || target.Platform != PlatformOpenAI || (target.Compact && !snapshot.CompactEnabled) {
		return body, application, nil
	}
	if !json.Valid(body) {
		return nil, application, fmt.Errorf("apply business system prompt: invalid JSON")
	}
	maxBytes := BusinessSystemPromptMaxBytes
	if snapshot.effectiveSHA256 != "" {
		maxBytes = BusinessSystemPromptBundleMaxBytes
	}
	hash, byteLength, err := validateBusinessSystemPromptBodyWithLimit(snapshot.Body, maxBytes)
	if err != nil {
		return nil, application, fmt.Errorf("%w: %v", ErrBusinessSystemPromptUnavailable, err)
	}
	expectedHash := snapshot.SHA256
	expectedLength := snapshot.ByteLength
	if snapshot.effectiveSHA256 != "" {
		expectedHash = snapshot.effectiveSHA256
		expectedLength = snapshot.effectiveByteLength
	}
	if expectedHash != "" && !strings.EqualFold(expectedHash, hash) {
		return nil, application, fmt.Errorf("%w: snapshot hash mismatch", ErrBusinessSystemPromptUnavailable)
	}
	if expectedLength > 0 && expectedLength != byteLength {
		return nil, application, fmt.Errorf("%w: snapshot length mismatch", ErrBusinessSystemPromptUnavailable)
	}

	application.Applied = true
	application.ServerInstructions = strings.TrimSpace(snapshot.Body)
	application.SHA256 = hash
	if application.BaseSHA256 == "" {
		application.BaseSHA256 = hash
	}
	if application.EffectiveSHA256 == "" {
		application.EffectiveSHA256 = hash
	}
	if application.EffectiveByteLength == 0 {
		application.EffectiveByteLength = byteLength
	}

	switch target.Protocol {
	case BusinessSystemPromptProtocolResponses:
		return applyBusinessSystemPromptInstructions(body, snapshot.Body, application)
	case BusinessSystemPromptProtocolChat:
		if instructions := gjson.GetBytes(body, "instructions"); instructions.Exists() {
			return applyBusinessSystemPromptInstructions(body, snapshot.Body, application)
		}
		return applyBusinessSystemPromptChatMessages(body, snapshot.Body, application)
	default:
		return nil, BusinessSystemPromptApplication{}, fmt.Errorf("unsupported business system prompt protocol %q", target.Protocol)
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
	if exposeServerPrompt || !application.Applied || application.Carrier != BusinessSystemPromptCarrierInstructions {
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
	if exposeServerPrompt || !application.Applied || application.Carrier != BusinessSystemPromptCarrierInstructions {
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
