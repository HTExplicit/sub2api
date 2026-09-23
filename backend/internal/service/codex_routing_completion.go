package service

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const codexRoutingProbeReadLimit = 128 << 10

// Only a delimited successful terminal and the original upstream model can
// certify a route. It does not retain prompts, reasoning, or response text.
type codexRoutingCompletion struct {
	Model     string
	Completed bool
	Failed    bool
	pending   []byte
}

func (o *codexRoutingCompletion) json(raw []byte) {
	var event struct {
		Type     string          `json:"type"`
		Object   string          `json:"object"`
		Status   string          `json:"status"`
		Model    string          `json:"model"`
		Error    json.RawMessage `json:"error"`
		Response *struct {
			Status string          `json:"status"`
			Model  string          `json:"model"`
			Error  json.RawMessage `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	badError := func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
	if badError(event.Error) || event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
		o.Failed = true
		return
	}
	model := ""
	if event.Type == "response.completed" && event.Response != nil {
		if event.Response.Status != "completed" || badError(event.Response.Error) {
			o.Failed = true
			return
		}
		model = strings.TrimSpace(event.Response.Model)
	} else if event.Object == "response" && event.Status == "completed" {
		model = strings.TrimSpace(event.Model)
	} else {
		return
	}
	if model == "" || len(model) > 256 || (o.Model != "" && o.Model != model) {
		o.Failed = true
		return
	}
	o.Model, o.Completed = model, true
}

func (o *codexRoutingCompletion) feed(data []byte) {
	if o.Failed {
		return
	}
	o.pending = append(o.pending, data...)
	o.pending = bytes.ReplaceAll(o.pending, []byte("\r\n"), []byte("\n"))
	for {
		end := bytes.Index(o.pending, []byte("\n\n"))
		if end < 0 {
			break
		}
		var lines []string
		for _, line := range strings.Split(string(o.pending[:end]), "\n") {
			if strings.HasPrefix(line, "data:") {
				lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		o.json([]byte(strings.Join(lines, "\n")))
		o.pending = o.pending[end+2:]
	}
	if len(o.pending) > codexRoutingProbeReadLimit {
		o.Failed = true
		o.pending = nil
	}
}

func readCodexRoutingCompletion(body io.Reader, contentType, requested string, observation *extensionv1.CodexRoutingObservation) error {
	data, err := io.ReadAll(io.LimitReader(body, codexRoutingProbeReadLimit+1))
	if err != nil || len(data) > codexRoutingProbeReadLimit {
		observation.Code = "routing_incomplete"
		return errCodexRoutingUnavailable
	}
	var completed codexRoutingCompletion
	// The ChatGPT edge omits Content-Type on streamed replies (2026-09-23);
	// an untyped body is still recognised by its SSE framing.
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || (strings.TrimSpace(contentType) == "" && bodyHasSSEFraming(data)) {
		completed.feed(data)
	} else {
		completed.json(data)
	}
	observation.Completed = completed.Completed && !completed.Failed
	observation.ResponseModel = completed.Model
	observation.ModelMatched = observation.Completed && completed.Model == requested
	if !observation.Completed {
		observation.Code = "routing_incomplete"
		return errCodexRoutingUnavailable
	}
	if !observation.ModelMatched {
		observation.Code = "routing_model_mismatch"
		return errCodexRoutingUnavailable
	}
	observation.Code = "routing_verified"
	return nil
}
