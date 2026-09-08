package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIContinuationDiagnosticBodyLimit = 8 << 20
	openAIContinuationDiagnosticItemLimit = 1024
	openAIContinuationDiagnosticJSONLimit = 6 << 10
)

// The diagnostic is an observation of an existing failed attempt. It is not
// a retry signal or a request replay record, and contains no arbitrary text.
type OpenAIContinuationDiagnostic struct {
	Version        int                            `json:"version"`
	Classification string                         `json:"classification"`
	UpstreamError  openAIContinuationErrorShape   `json:"upstream_error"`
	Incoming       openAIContinuationRequestShape `json:"incoming"`
	Wire           openAIContinuationRequestShape `json:"wire"`
}

type openAIContinuationFingerprint struct {
	Kind       string `json:"kind"`
	Bytes      int    `json:"bytes"`
	Characters int    `json:"characters"`
	SHA256     string `json:"sha256,omitempty"`
	Value      string `json:"value,omitempty"`
}

type openAIContinuationErrorShape struct {
	ErrorType         openAIContinuationFingerprint `json:"error_type"`
	ErrorCode         openAIContinuationFingerprint `json:"error_code"`
	ErrorParam        openAIContinuationFingerprint `json:"error_param"`
	Message           openAIContinuationFingerprint `json:"message"`
	Hints             []string                      `json:"hints,omitempty"`
	InspectionLimited bool                          `json:"inspection_limited"`
}

type openAIContinuationRequestShape struct {
	BodyBytes             int                           `json:"body_bytes"`
	BodySource            string                        `json:"body_source"`
	InspectionLimited     bool                          `json:"inspection_limited"`
	PromptCache           openAIContinuationFingerprint `json:"prompt_cache"`
	Instructions          openAIContinuationFingerprint `json:"instructions"`
	PreviousResponse      openAIContinuationFingerprint `json:"previous_response"`
	Session               openAIContinuationFingerprint `json:"session"`
	Conversation          openAIContinuationFingerprint `json:"conversation"`
	ClientMetadataSession openAIContinuationFingerprint `json:"client_metadata_session"`
	History               openAIContinuationHistory     `json:"history"`
}

type openAIContinuationHistory struct {
	Items              int    `json:"items"`
	Messages           int    `json:"messages"`
	Reasoning          int    `json:"reasoning"`
	Compaction         int    `json:"compaction"`
	References         int    `json:"references"`
	Calls              int    `json:"calls"`
	Outputs            int    `json:"outputs"`
	Other              int    `json:"other"`
	Encrypted          int    `json:"encrypted"`
	MissingCallIDs     int    `json:"missing_call_ids"`
	MissingOutputIDs   int    `json:"missing_output_ids"`
	DuplicateCallIDs   int    `json:"duplicate_call_ids"`
	DuplicateOutputIDs int    `json:"duplicate_output_ids"`
	UnmatchedOutputs   int    `json:"unmatched_outputs"`
	UnpairedCalls      int    `json:"unpaired_calls"`
	ScanLimited        bool   `json:"scan_limited"`
	CallIDsSHA256      string `json:"call_ids_sha256,omitempty"`
	OutputIDsSHA256    string `json:"output_ids_sha256,omitempty"`
	EncryptedSHA256    string `json:"encrypted_sha256,omitempty"`
}

func buildOpenAIContinuationDiagnostic(c *gin.Context, incomingBody []byte, upstreamReq *http.Request, preparedBody, upstreamError []byte, classification string) *OpenAIContinuationDiagnostic {
	var incomingHeaders, wireHeaders http.Header
	if c != nil && c.Request != nil {
		incomingHeaders = c.Request.Header
	}
	if upstreamReq != nil {
		wireHeaders = upstreamReq.Header
	}
	wireBody, wireSource, limited := continuationDiagnosticWireBody(upstreamReq, preparedBody)
	diagnostic := &OpenAIContinuationDiagnostic{
		Version: 1, Classification: continuationDiagnosticClassification(classification),
		UpstreamError: continuationDiagnosticError(upstreamError),
		// This is the forwarding function's entry snapshot, not a claim that
		// earlier handler/protocol normalization has never run.
		Incoming: continuationDiagnosticRequest(incomingBody, incomingHeaders, "forwarding_entry"),
		Wire:     continuationDiagnosticRequest(wireBody, wireHeaders, wireSource),
	}
	diagnostic.Wire.InspectionLimited = diagnostic.Wire.InspectionLimited || limited
	if limited && upstreamReq != nil && upstreamReq.ContentLength > int64(diagnostic.Wire.BodyBytes) {
		// LimitReader bounds inspection, not the reported size when the built
		// request already has a known Content-Length.
		diagnostic.Wire.BodyBytes = int(upstreamReq.ContentLength)
	}
	return sanitizeOpenAIContinuationDiagnostic(diagnostic)
}

func continuationDiagnosticWireBody(req *http.Request, fallback []byte) ([]byte, string, bool) {
	if req == nil || req.GetBody == nil {
		return fallback, "prepared_fallback", true
	}
	copyBody, err := req.GetBody()
	if err != nil || copyBody == nil {
		return nil, "actual_request_unavailable", true
	}
	defer func() { _ = copyBody.Close() }()
	body, err := io.ReadAll(io.LimitReader(copyBody, openAIContinuationDiagnosticBodyLimit+1))
	if err != nil {
		return nil, "actual_request_unavailable", true
	}
	return body, "actual_request", len(body) > openAIContinuationDiagnosticBodyLimit
}

func continuationDiagnosticFingerprint(value gjson.Result) openAIContinuationFingerprint {
	if !value.Exists() {
		return openAIContinuationFingerprint{Kind: "missing"}
	}
	kind, raw := "unknown", value.Raw
	switch value.Type {
	case gjson.String:
		kind, raw = "string", value.String()
	case gjson.Null:
		kind = "null"
	case gjson.Number:
		kind = "number"
	case gjson.True, gjson.False:
		kind = "boolean"
	case gjson.JSON:
		if value.IsArray() {
			kind = "array"
		} else {
			kind = "object"
		}
	}
	digest := sha256.Sum256([]byte(raw))
	return openAIContinuationFingerprint{Kind: kind, Bytes: len(raw), Characters: utf8.RuneCountInString(raw), SHA256: hex.EncodeToString(digest[:])}
}

func continuationDiagnosticHeader(headers http.Header, name string) openAIContinuationFingerprint {
	values := headers.Values(name)
	if len(values) == 0 {
		return openAIContinuationFingerprint{Kind: "missing"}
	}
	return continuationDiagnosticFingerprint(gjson.Result{Type: gjson.String, Str: values[0]})
}

func continuationDiagnosticRequest(body []byte, headers http.Header, source string) openAIContinuationRequestShape {
	shape := openAIContinuationRequestShape{
		BodyBytes: len(body), BodySource: source,
		Session:      continuationDiagnosticHeader(headers, "session_id"),
		Conversation: continuationDiagnosticHeader(headers, "conversation_id"),
	}
	if len(body) > openAIContinuationDiagnosticBodyLimit || !gjson.ValidBytes(body) {
		shape.InspectionLimited = true
		shape.PromptCache.Kind, shape.Instructions.Kind, shape.PreviousResponse.Kind, shape.ClientMetadataSession.Kind = "uninspected", "uninspected", "uninspected", "uninspected"
		return shape
	}
	fields := gjson.GetManyBytes(body, "prompt_cache_key", "instructions", "previous_response_id", "client_metadata.session_id", "input")
	shape.PromptCache = continuationDiagnosticFingerprint(fields[0])
	shape.Instructions = continuationDiagnosticFingerprint(fields[1])
	shape.PreviousResponse = continuationDiagnosticFingerprint(fields[2])
	shape.ClientMetadataSession = continuationDiagnosticFingerprint(fields[3])
	shape.History = continuationDiagnosticHistory(fields[4])
	return shape
}

func continuationDiagnosticHashPart(h hash.Hash, text string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(text)))
	_, _ = h.Write(length[:])
	_, _ = io.WriteString(h, text)
}

func continuationDiagnosticHistory(input gjson.Result) openAIContinuationHistory {
	var shape openAIContinuationHistory
	if !input.IsArray() {
		return shape
	}
	calls, outputs := make(map[[32]byte]int), make(map[[32]byte]int)
	callDigest, outputDigest, encryptedDigest := sha256.New(), sha256.New(), sha256.New()
	input.ForEach(func(_, item gjson.Result) bool {
		if shape.Items == openAIContinuationDiagnosticItemLimit {
			shape.ScanLimited = true
			return false
		}
		shape.Items++
		switch item.Get("type").String() {
		case "message", "":
			if item.Get("role").Type == gjson.String || item.Get("type").String() == "message" {
				shape.Messages++
			} else {
				shape.Other++
			}
		case "reasoning":
			shape.Reasoning++
		case "compaction", "compaction_summary":
			shape.Compaction++
		case "item_reference":
			shape.References++
		case "function_call", "custom_tool_call":
			shape.Calls++
			id := item.Get("call_id")
			if id.Type != gjson.String || id.String() == "" {
				shape.MissingCallIDs++
				break
			}
			text := id.String()
			calls[sha256.Sum256([]byte(text))]++
			continuationDiagnosticHashPart(callDigest, text)
		case "function_call_output", "custom_tool_call_output":
			shape.Outputs++
			id := item.Get("call_id")
			if id.Type != gjson.String || id.String() == "" {
				shape.MissingOutputIDs++
				break
			}
			text := id.String()
			outputs[sha256.Sum256([]byte(text))]++
			continuationDiagnosticHashPart(outputDigest, text)
		default:
			shape.Other++
		}
		if encrypted := item.Get("encrypted_content"); encrypted.Type == gjson.String && encrypted.String() != "" {
			shape.Encrypted++
			continuationDiagnosticHashPart(encryptedDigest, encrypted.String())
		}
		return true
	})
	for id, count := range calls {
		shape.DuplicateCallIDs += count - 1
		if outputs[id] == 0 {
			shape.UnpairedCalls += count
		}
	}
	for id, count := range outputs {
		shape.DuplicateOutputIDs += count - 1
		if calls[id] == 0 {
			shape.UnmatchedOutputs += count
		}
	}
	if len(calls) > 0 {
		shape.CallIDsSHA256 = hex.EncodeToString(callDigest.Sum(nil))
	}
	if len(outputs) > 0 {
		shape.OutputIDsSHA256 = hex.EncodeToString(outputDigest.Sum(nil))
	}
	if shape.Encrypted > 0 {
		shape.EncryptedSHA256 = hex.EncodeToString(encryptedDigest.Sum(nil))
	}
	return shape
}

var continuationDiagnosticParam = regexp.MustCompile(`^(?:model|instructions|prompt_cache_key|previous_response_id|store|stream|input|tools|reasoning(?:\.(?:effort|mode|context))?|(?:input|tools)(?:\[[0-9]{1,6}\]|\.[0-9]{1,6})(?:\.(?:id|call_id|type|role|content|encrypted_content|name|namespace|arguments|output|function)(?:\.(?:name|arguments))?)?)$`)

func continuationDiagnosticKnownError(value string) string {
	switch value {
	case "invalid_request_error", "invalid_argument", "bad_request", "upstream_error", "server_error", "validation_error", "invalid_request", "BadRequest",
		"previous_response_not_found", "invalid_encrypted_content", "thinking_signature_invalid", "model_not_found", "unsupported_parameter", "unknown_parameter", "invalid_value", "missing_required_parameter", "context_length_exceeded", "400":
		return strings.Clone(value)
	default:
		return ""
	}
}

var continuationDiagnosticHints = []string{
	"previous_response_not_found", "previous response not found", "invalid_encrypted_content", "encrypted content could not be verified",
	"thinking_signature_invalid", "unknown parameter", "unsupported", "missing", "function_call_output", "call_id", "instructions", "prompt_cache_key",
}

func continuationDiagnosticError(body []byte) openAIContinuationErrorShape {
	if len(body) > openAIContinuationDiagnosticBodyLimit {
		return openAIContinuationErrorShape{InspectionLimited: true}
	}
	root := gjson.ParseBytes(body)
	envelope := root.Get("error")
	if !envelope.IsObject() {
		envelope = root.Get("response.error")
	}
	if !envelope.IsObject() {
		envelope = root
	}
	typ, code, param, message := envelope.Get("type"), envelope.Get("code"), envelope.Get("param"), envelope.Get("message")
	if !gjson.ValidBytes(body) {
		typ, code, param = gjson.Result{}, gjson.Result{}, gjson.Result{}
		message = gjson.Result{Type: gjson.String, Str: string(body)}
	} else if root.Type == gjson.String {
		message = root
	}
	shape := openAIContinuationErrorShape{
		ErrorType: continuationDiagnosticFingerprint(typ), ErrorCode: continuationDiagnosticFingerprint(code),
		ErrorParam: continuationDiagnosticFingerprint(param), Message: continuationDiagnosticFingerprint(message),
	}
	if typ.Type == gjson.String {
		shape.ErrorType.Value = continuationDiagnosticKnownError(typ.String())
	}
	if code.Type == gjson.String {
		shape.ErrorCode.Value = continuationDiagnosticKnownError(code.String())
	}
	if param.Type == gjson.String && len(param.String()) <= 96 && continuationDiagnosticParam.MatchString(param.String()) {
		shape.ErrorParam.Value = strings.Clone(param.String())
	}
	if message.Type == gjson.String {
		lower := strings.ToLower(message.String())
		for _, hint := range continuationDiagnosticHints {
			if strings.Contains(lower, hint) {
				shape.Hints = append(shape.Hints, hint)
			}
		}
	}
	return shape
}

func continuationDiagnosticClassification(value string) string {
	switch value {
	case "previous_response_not_found", "invalid_encrypted_content", "thinking_signature_invalid",
		"request_validation", "unclassified_bad_request", "opaque_tool_chain_400":
		// opaque_tool_chain_400 remains readable for historical diagnostics only.
		return value
	default:
		return "unclassified"
	}
}

// Queue-bound validation is independent of best-effort text redaction. No
// producer can smuggle arbitrary strings through this typed metadata field.
func sanitizeOpenAIContinuationDiagnostic(in *OpenAIContinuationDiagnostic) *OpenAIContinuationDiagnostic {
	if in == nil {
		return nil
	}
	out := *in
	out.Version = 1
	out.Classification = continuationDiagnosticClassification(in.Classification)
	cleanFingerprint := func(f openAIContinuationFingerprint) openAIContinuationFingerprint {
		switch f.Kind {
		case "missing", "null", "string", "number", "boolean", "array", "object", "uninspected":
		default:
			f.Kind = "unknown"
		}
		if f.Bytes < 0 {
			f.Bytes = 0
		}
		if f.Characters < 0 {
			f.Characters = 0
		}
		f.SHA256 = continuationDiagnosticSafeHash(f.SHA256)
		f.Value = ""
		return f
	}
	cleanRequest := func(r openAIContinuationRequestShape) openAIContinuationRequestShape {
		switch r.BodySource {
		case "forwarding_entry", "actual_request", "prepared_fallback", "actual_request_unavailable":
		default:
			r.BodySource = "unknown"
		}
		if r.BodyBytes < 0 {
			r.BodyBytes = 0
		}
		r.PromptCache, r.Instructions, r.PreviousResponse = cleanFingerprint(r.PromptCache), cleanFingerprint(r.Instructions), cleanFingerprint(r.PreviousResponse)
		r.Session, r.Conversation, r.ClientMetadataSession = cleanFingerprint(r.Session), cleanFingerprint(r.Conversation), cleanFingerprint(r.ClientMetadataSession)
		r.History.CallIDsSHA256 = continuationDiagnosticSafeHash(r.History.CallIDsSHA256)
		r.History.OutputIDsSHA256 = continuationDiagnosticSafeHash(r.History.OutputIDsSHA256)
		r.History.EncryptedSHA256 = continuationDiagnosticSafeHash(r.History.EncryptedSHA256)
		return r
	}
	out.Incoming, out.Wire = cleanRequest(in.Incoming), cleanRequest(in.Wire)
	out.UpstreamError.ErrorType, out.UpstreamError.ErrorCode = cleanFingerprint(in.UpstreamError.ErrorType), cleanFingerprint(in.UpstreamError.ErrorCode)
	out.UpstreamError.ErrorType.Value = continuationDiagnosticKnownError(in.UpstreamError.ErrorType.Value)
	out.UpstreamError.ErrorCode.Value = continuationDiagnosticKnownError(in.UpstreamError.ErrorCode.Value)
	out.UpstreamError.ErrorParam, out.UpstreamError.Message = cleanFingerprint(in.UpstreamError.ErrorParam), cleanFingerprint(in.UpstreamError.Message)
	param := in.UpstreamError.ErrorParam.Value
	if len(param) <= 96 && continuationDiagnosticParam.MatchString(param) {
		out.UpstreamError.ErrorParam.Value = strings.Clone(param)
	}
	out.UpstreamError.Hints = nil
	for _, allowed := range continuationDiagnosticHints {
		for _, hint := range in.UpstreamError.Hints {
			if hint == allowed {
				out.UpstreamError.Hints = append(out.UpstreamError.Hints, allowed)
				break
			}
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil || len(encoded) > openAIContinuationDiagnosticJSONLimit {
		return nil
	}
	return &out
}

func continuationDiagnosticSafeHash(value string) string {
	if len(value) != 64 {
		return ""
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return ""
		}
	}
	return strings.Clone(value)
}
