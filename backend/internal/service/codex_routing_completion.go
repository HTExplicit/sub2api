package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

const codexRoutingProbeReadLimit = 128 << 10

var ErrCodexRoutingModelMismatch = errors.New("upstream Codex response model does not match the requested model")

// Only bounded metadata is retained. Probe totals remain limited to 128 KiB;
// business readers explicitly select their existing stream/JSON read limits.
type codexRoutingCompletion struct {
	Model          string
	RequestedModel string
	Completed      bool
	Failed         bool
	Mismatch       bool
	FailureCode    string
	maxEventBytes  int
	pending        []byte
	scanFrom       int
}

func (o *codexRoutingCompletion) fail(code string) {
	o.Failed = true
	if o.FailureCode == "" {
		o.FailureCode = code
	}
}

func (o *codexRoutingCompletion) observeModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if len(model) > 256 {
		o.fail("routing_incomplete")
		return
	}
	if (o.Model != "" && o.Model != model) || (o.RequestedModel != "" && o.RequestedModel != model) {
		o.Mismatch = true
	}
	o.Model = model
}

func (o *codexRoutingCompletion) headers(headers http.Header) {
	for name, values := range headers {
		if strings.EqualFold(name, "openai-model") || strings.EqualFold(name, "x-openai-model") {
			for _, value := range values {
				o.observeModel(value)
			}
		}
	}
}

// Classification uses explicit upstream codes, never error prose, account plan,
// STATE length, or the official client's hard-coded safety-reroute warning.
func codexRoutingErrorCode(raw json.RawMessage) string {
	var value struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &value) == nil {
		for _, code := range []string{value.Code, value.Type} {
			switch code {
			case "server_is_overloaded", "rate_limit_exceeded", "slow_down", "insufficient_quota", "usage_limit_reached":
				return "routing_capacity"
			case "cyber_policy", "bio_policy", "misalignment_policy_violation", "content_policy_violation":
				return "routing_policy"
			}
		}
	}
	return "routing_upstream"
}

func (o *codexRoutingCompletion) json(raw []byte) { o.jsonEvent(raw, "") }

func (o *codexRoutingCompletion) jsonEvent(raw []byte, eventType string) {
	var event struct {
		Type     string                     `json:"type"`
		Object   string                     `json:"object"`
		Status   string                     `json:"status"`
		Model    string                     `json:"model"`
		Headers  map[string]json.RawMessage `json:"headers"`
		Error    json.RawMessage            `json:"error"`
		Response *struct {
			Status  string                     `json:"status"`
			Model   string                     `json:"model"`
			Headers map[string]json.RawMessage `json:"headers"`
			Error   json.RawMessage            `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if event.Type == "" {
		event.Type = eventType
	}
	observeHeaders := func(headers map[string]json.RawMessage) {
		for name, value := range headers {
			if strings.EqualFold(name, "openai-model") || strings.EqualFold(name, "x-openai-model") {
				var model string
				if json.Unmarshal(value, &model) == nil {
					o.observeModel(model)
				}
			}
		}
	}
	observeHeaders(event.Headers)
	if event.Response != nil {
		observeHeaders(event.Response.Headers)
		o.observeModel(event.Response.Model)
	} else if event.Object == "response" {
		o.observeModel(event.Model)
	}
	badError := func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
	if badError(event.Error) {
		o.fail(codexRoutingErrorCode(event.Error))
		return
	}
	if event.Response != nil && badError(event.Response.Error) {
		o.fail(codexRoutingErrorCode(event.Response.Error))
		return
	}
	switch event.Type {
	case "response.cancelled", "response.canceled":
		o.fail("routing_cancelled")
		return
	case "response.incomplete":
		o.fail("routing_incomplete")
		return
	case "error", "response.failed":
		o.fail("routing_upstream")
		return
	}
	terminal := event.Type == "response.completed" && event.Response != nil
	if terminal {
		if event.Response.Status != "completed" {
			o.fail("routing_incomplete")
			return
		}
	} else if event.Object == "response" && event.Status == "completed" {
		terminal = true
	} else {
		return
	}
	if terminal && o.Model != "" {
		o.Completed = true
	} else {
		o.fail("routing_incomplete")
	}
}

func (o *codexRoutingCompletion) feed(data []byte) {
	if o.Failed {
		return
	}
	limit := o.maxEventBytes
	if limit <= 0 {
		limit = codexRoutingProbeReadLimit
	}
	o.pending = append(o.pending, data...)
	for i := o.scanFrom; i < len(o.pending); i++ {
		if o.pending[i] != '\n' {
			continue
		}
		next := i + 1
		if next < len(o.pending) && o.pending[next] == '\r' {
			next++
		}
		if next >= len(o.pending) || o.pending[next] != '\n' {
			continue
		}
		if i > limit {
			o.fail("routing_incomplete")
			break
		}
		var lines [][]byte
		eventType := ""
		for _, line := range bytes.Split(o.pending[:i], []byte{'\n'}) {
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if bytes.HasPrefix(line, []byte("data:")) {
				lines = append(lines, bytes.TrimSpace(line[len("data:"):]))
			} else if bytes.HasPrefix(line, []byte("event:")) {
				eventType = string(bytes.TrimSpace(line[len("event:"):]))
			}
		}
		o.jsonEvent(bytes.Join(lines, []byte{'\n'}), eventType)
		o.pending = o.pending[next+1:]
		i = -1
		if o.Failed {
			break
		}
	}
	o.scanFrom = max(0, len(o.pending)-2)
	if len(o.pending) > limit {
		o.fail("routing_incomplete")
	}
	if o.Failed {
		o.pending = nil
	}
}

func (o *codexRoutingCompletion) observationCode(requested string, status int) string {
	if o.FailureCode != "" {
		return o.FailureCode
	}
	if status != http.StatusOK && status != http.StatusSwitchingProtocols {
		return "routing_upstream"
	}
	if o.Mismatch || (o.Model != "" && o.Model != requested) {
		return "routing_model_mismatch"
	}
	if !o.Completed || o.Failed || o.Model == "" {
		return "routing_incomplete"
	}
	return "routing_verified"
}

func readCodexRoutingCompletion(body io.Reader, contentType, requested string, observation *extensionv1.CodexRoutingObservation, headers ...http.Header) error {
	data, err := io.ReadAll(io.LimitReader(body, codexRoutingProbeReadLimit+1))
	if err != nil || len(data) > codexRoutingProbeReadLimit {
		observation.Code = "routing_incomplete"
		if errors.Is(err, context.Canceled) {
			observation.Code = "routing_cancelled"
		}
		return errCodexRoutingUnavailable
	}
	completed := codexRoutingCompletion{RequestedModel: requested}
	for _, header := range headers {
		completed.headers(header)
	}
	// The ChatGPT edge may omit Content-Type on otherwise valid SSE.
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || (strings.TrimSpace(contentType) == "" && bodyHasSSEFraming(data)) {
		completed.feed(data)
	} else {
		completed.json(data)
	}
	observation.Completed = completed.Completed && !completed.Failed
	observation.ResponseModel = completed.Model
	observation.ModelMatched = observation.Completed && !completed.Mismatch && completed.Model == requested
	observation.Code = completed.observationCode(requested, http.StatusOK)
	if observation.Code != "routing_verified" {
		return errCodexRoutingUnavailable
	}
	return nil
}
