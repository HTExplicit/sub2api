package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Each final protocol has exactly one insertion point: the system
// instructions (prepend or append), or a leading control message when the
// role requires one. The functions are pure; callers pass the final body.

var errSystemPromptCarrier = errors.New("system prompt carrier has an unsupported shape")

func joinSystemPromptText(prompt SystemPrompt, text string) string {
	switch {
	case text == "":
		return prompt.Body
	case prompt.Position == SystemPromptPositionPrepend:
		return prompt.Body + "\n\n" + text
	default:
		return text + "\n\n" + prompt.Body
	}
}

// leadingControlMessages counts the leading system/developer messages; an
// append placement goes right after them.
func leadingControlMessages(items []gjson.Result) int {
	count := 0
	for count < len(items) {
		role := items[count].Get("role").String()
		if role != "system" && role != "developer" {
			break
		}
		count++
	}
	return count
}

// insertJSONArrayItem keeps every existing element byte-for-byte; re-encoding
// would rewrite customer content (for example HTML escaping) that retries and
// reasoning recovery compare by bytes.
func insertJSONArrayItem(body []byte, path string, items []gjson.Result, at int, item []byte) ([]byte, error) {
	var out bytes.Buffer
	_ = out.WriteByte('[')
	count := 0
	write := func(raw string) {
		if count > 0 {
			_ = out.WriteByte(',')
		}
		_, _ = out.WriteString(raw)
		count++
	}
	for index, existing := range items {
		if index == at {
			write(string(item))
		}
		write(existing.Raw)
	}
	if at >= len(items) {
		write(string(item))
	}
	_ = out.WriteByte(']')
	return sjson.SetRawBytes(body, path, out.Bytes())
}

// systemPromptEcho remembers what one Responses send changed, so the echoed
// instructions, integrity checks and retry indices can refer to the client's
// request instead of the injected one.
type systemPromptEcho struct {
	instructions        bool
	finalInstructions   string
	clientInstructions  string
	clientHadInstructs  bool
	inputIndex          int
	inputWasString      bool
	clientInputText     string
	developerPromptText string
}

// injectResponsesSystemPrompt: auto/system extend instructions; developer adds
// a developer input item first (prepend) or after the leading control items.
func injectResponsesSystemPrompt(body []byte, prompt SystemPrompt) ([]byte, *systemPromptEcho, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, nil, errSystemPromptCarrier
	}
	echo := &systemPromptEcho{inputIndex: -1}
	if prompt.Role != SystemPromptRoleDeveloper {
		current := gjson.GetBytes(body, "instructions")
		if current.Exists() && current.Type != gjson.String && current.Type != gjson.Null {
			return nil, nil, errSystemPromptCarrier
		}
		echo.instructions = true
		echo.clientHadInstructs = current.Type == gjson.String
		echo.clientInstructions = current.String()
		echo.finalInstructions = joinSystemPromptText(prompt, echo.clientInstructions)
		updated, err := sjson.SetBytes(body, "instructions", echo.finalInstructions)
		return updated, echo, err
	}
	item, err := json.Marshal(map[string]any{
		"type": "message", "role": "developer",
		"content": []map[string]string{{"type": "input_text", "text": prompt.Body}},
	})
	if err != nil {
		return nil, nil, err
	}
	echo.developerPromptText = prompt.Body
	input := gjson.GetBytes(body, "input")
	var items []gjson.Result
	switch {
	case !input.Exists() || input.Type == gjson.Null:
	case input.Type == gjson.String:
		user, marshalErr := json.Marshal(map[string]string{"role": "user", "content": input.String()})
		if marshalErr != nil {
			return nil, nil, marshalErr
		}
		echo.inputWasString, echo.clientInputText = true, input.String()
		items = []gjson.Result{{Type: gjson.JSON, Raw: string(user)}}
	case input.IsArray():
		items = input.Array()
	default:
		return nil, nil, errSystemPromptCarrier
	}
	at := 0
	if prompt.Position != SystemPromptPositionPrepend {
		at = leadingControlMessages(items)
	}
	echo.inputIndex = at
	updated, err := insertJSONArrayItem(body, "input", items, at, item)
	return updated, echo, err
}

// injectChatSystemPrompt inserts one system (or developer) message. Upstreams
// that reject the developer role receive it as system.
func injectChatSystemPrompt(body []byte, prompt SystemPrompt, systemRoleOnly bool) ([]byte, error) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return nil, errSystemPromptCarrier
	}
	role := "system"
	if prompt.Role == SystemPromptRoleDeveloper && !systemRoleOnly {
		role = "developer"
	}
	item, err := json.Marshal(map[string]string{"role": role, "content": prompt.Body})
	if err != nil {
		return nil, err
	}
	items := messages.Array()
	at := 0
	if prompt.Position != SystemPromptPositionPrepend {
		at = leadingControlMessages(items)
	}
	return insertJSONArrayItem(body, "messages", items, at, item)
}

// anthropicSystemWithPrompt returns the new top-level Anthropic system value.
// Every role is delivered as system text.
func anthropicSystemWithPrompt(system json.RawMessage, prompt SystemPrompt) (json.RawMessage, error) {
	current := gjson.ParseBytes(system)
	switch {
	case len(system) == 0 || current.Type == gjson.Null:
		return json.Marshal(prompt.Body)
	case current.Type == gjson.String:
		return json.Marshal(joinSystemPromptText(prompt, current.String()))
	case current.IsArray():
		block, err := json.Marshal(map[string]string{"type": "text", "text": prompt.Body})
		if err != nil {
			return nil, err
		}
		items := current.Array()
		at := len(items)
		if prompt.Position == SystemPromptPositionPrepend {
			at = 0
		}
		out, err := insertJSONArrayItem([]byte(`{}`), "system", items, at, block)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(gjson.GetBytes(out, "system").Raw), nil
	default:
		return nil, errSystemPromptCarrier
	}
}

func injectAnthropicSystemPrompt(body []byte, prompt SystemPrompt) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, errSystemPromptCarrier
	}
	system, err := anthropicSystemWithPrompt(json.RawMessage(gjson.GetBytes(body, "system").Raw), prompt)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "system", system)
}

// injectGeminiSystemPrompt adds a text part to systemInstruction (or the
// snake_case system_instruction the client used).
func injectGeminiSystemPrompt(body []byte, prompt SystemPrompt) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, errSystemPromptCarrier
	}
	field := "systemInstruction"
	if gjson.GetBytes(body, "system_instruction").Exists() && !gjson.GetBytes(body, field).Exists() {
		field = "system_instruction"
	}
	instruction := gjson.GetBytes(body, field)
	if instruction.Exists() && instruction.Type != gjson.Null && !instruction.IsObject() {
		return nil, errSystemPromptCarrier
	}
	parts := instruction.Get("parts")
	if parts.Exists() && !parts.IsArray() {
		return nil, errSystemPromptCarrier
	}
	part, err := json.Marshal(map[string]string{"text": prompt.Body})
	if err != nil {
		return nil, err
	}
	items := parts.Array()
	at := len(items)
	if prompt.Position == SystemPromptPositionPrepend {
		at = 0
	}
	if !instruction.IsObject() {
		return sjson.SetRawBytes(body, field, []byte(`{"parts":[`+string(part)+`]}`))
	}
	return insertJSONArrayItem(body, field+".parts", items, at, part)
}

// injectGeminiEnvelopeSystemPrompt handles the Code Assist/Antigravity
// envelope whose Gemini request lives under "request".
func injectGeminiEnvelopeSystemPrompt(body []byte, prompt SystemPrompt) ([]byte, error) {
	inner := gjson.GetBytes(body, "request")
	if !inner.IsObject() {
		return injectGeminiSystemPrompt(body, prompt)
	}
	updated, err := injectGeminiSystemPrompt([]byte(inner.Raw), prompt)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "request", updated)
}

func (s *SystemPromptService) observe(c *gin.Context, account *Account, protocol string, prompt SystemPrompt, err error) {
	if err != nil {
		slog.Warn("system_prompt.skipped", "account_id", account.ID, "protocol", protocol, "prompt_id", prompt.ID, "error", err)
		return
	}
	slog.Debug("system_prompt.applied", "account_id", account.ID, "protocol", protocol, "prompt_id", prompt.ID,
		"position", prompt.Position, "role", prompt.Role, "client_request", c != nil)
}

// clientPrompt returns the prompt for a client request. Internal sends such
// as account tests have no gin context, and Responses compaction (including
// its protocol fallbacks) keeps its own instructions; neither is injected.
func (s *SystemPromptService) clientPrompt(c *gin.Context, account *Account) (SystemPrompt, bool) {
	if c == nil || isOpenAIResponsesCompactPath(c) {
		return SystemPrompt{}, false
	}
	return s.resolve(account)
}

// ApplyAnthropic injects into a final Anthropic Messages body.
func (s *SystemPromptService) ApplyAnthropic(c *gin.Context, account *Account, body []byte) []byte {
	updated, _ := s.applyAnthropic(c, account, body)
	return updated
}

func (s *SystemPromptService) applyAnthropic(c *gin.Context, account *Account, body []byte) ([]byte, bool) {
	prompt, ok := s.clientPrompt(c, account)
	if !ok {
		return body, false
	}
	updated, err := injectAnthropicSystemPrompt(body, prompt)
	s.observe(c, account, "messages", prompt, err)
	if err != nil {
		return body, false
	}
	return updated, true
}

// ApplyAnthropicSystem injects into a converted request's system value before
// Claude OAuth mimicry relocates the client system.
func (s *SystemPromptService) ApplyAnthropicSystem(c *gin.Context, account *Account, system json.RawMessage) json.RawMessage {
	prompt, ok := s.clientPrompt(c, account)
	if !ok {
		return system
	}
	updated, err := anthropicSystemWithPrompt(system, prompt)
	s.observe(c, account, "messages", prompt, err)
	if err != nil {
		return system
	}
	return updated
}

// ApplyChat injects into a final Chat Completions body.
func (s *SystemPromptService) ApplyChat(c *gin.Context, account *Account, body []byte, systemRoleOnly bool) []byte {
	prompt, ok := s.clientPrompt(c, account)
	if !ok {
		return body
	}
	updated, err := injectChatSystemPrompt(body, prompt, systemRoleOnly)
	s.observe(c, account, "chat", prompt, err)
	if err != nil {
		return body
	}
	return updated
}

// ApplyGemini injects into a final Gemini generateContent body, including the
// Code Assist envelope.
func (s *SystemPromptService) ApplyGemini(c *gin.Context, account *Account, body []byte) []byte {
	prompt, ok := s.clientPrompt(c, account)
	if !ok {
		return body
	}
	updated, err := injectGeminiEnvelopeSystemPrompt(body, prompt)
	s.observe(c, account, "gemini", prompt, err)
	if err != nil {
		return body
	}
	return updated
}

// applyResponses injects into a final Responses body. Compact requests and
// Codex Responses Lite requests are never injected.
func (s *SystemPromptService) applyResponses(c *gin.Context, account *Account, body []byte) []byte {
	setSystemPromptEcho(c, nil)
	if c == nil || isOpenAIResponsesLiteHeader(c.GetHeader(responsesLiteHeader)) || isOpenAIResponsesLiteWebSocketPayload(body) {
		return body
	}
	prompt, ok := s.clientPrompt(c, account)
	if !ok {
		return body
	}
	updated, echo, err := injectResponsesSystemPrompt(body, prompt)
	s.observe(c, account, "responses", prompt, err)
	if err != nil {
		return body
	}
	setSystemPromptEcho(c, echo)
	return updated
}

func (s *GatewayService) SetSystemPromptService(prompts *SystemPromptService) {
	if s != nil {
		s.systemPrompts = prompts
	}
}

func (s *OpenAIGatewayService) SetSystemPromptService(prompts *SystemPromptService) {
	if s != nil {
		s.systemPrompts = prompts
	}
}

func (s *GeminiMessagesCompatService) SetSystemPromptService(prompts *SystemPromptService) {
	if s != nil {
		s.systemPrompts = prompts
	}
}

func (s *AntigravityGatewayService) SetSystemPromptService(prompts *SystemPromptService) {
	if s != nil {
		s.systemPrompts = prompts
	}
}

// applySystemPromptToParsed injects into a native Messages request once per
// account attempt (the handler clones the parsed request per attempt), before
// passthrough, Bedrock or Claude OAuth mimicry take the body.
func (s *GatewayService) applySystemPromptToParsed(c *gin.Context, account *Account, parsed *ParsedRequest) error {
	if s == nil || parsed == nil || parsed.Body == nil {
		return nil
	}
	updated, applied := s.systemPrompts.applyAnthropic(c, account, parsed.Body.Bytes())
	if !applied {
		return nil
	}
	if err := parsed.ReplaceBody(updated); err != nil {
		return fmt.Errorf("rewrite request body: %w", err)
	}
	return nil
}

const systemPromptEchoContextKey = "system_prompt_echo"

func setSystemPromptEcho(c *gin.Context, echo *systemPromptEcho) {
	if c != nil {
		c.Set(systemPromptEchoContextKey, echo)
	}
}

func systemPromptEchoFrom(c *gin.Context) *systemPromptEcho {
	if c == nil {
		return nil
	}
	value, _ := c.Get(systemPromptEchoContextKey)
	echo, _ := value.(*systemPromptEcho)
	return echo
}

func systemPromptInputPath(index int) string {
	return "input." + strconv.Itoa(index)
}
