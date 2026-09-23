package service

import (
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type codexIdentityBodyKey struct{}

type CodexIdentityFieldObservation struct {
	HeaderDigest string `json:"header_digest,omitempty"`
	BodyDigest   string `json:"body_digest,omitempty"`
	Consistency  string `json:"consistency"`
}

type codexIdentityBodyObservation struct {
	Inspected bool
	Fields    map[string]string
}

func inspectCodexIdentityBody(request *http.Request) codexIdentityBodyObservation {
	result := codexIdentityBodyObservation{Fields: map[string]string{}}
	if request == nil || request.GetBody == nil || request.Header.Get("Content-Encoding") != "" {
		return result
	}
	body, err := request.GetBody()
	if err != nil {
		return result
	}
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(body, codexRoutingProbeReadLimit+1))
	if err != nil || len(raw) > codexRoutingProbeReadLimit || !gjson.ValidBytes(raw) {
		return result
	}
	result.Inspected = true
	paths := map[string]string{"installation": "client_metadata.x-codex-installation-id", "session": "client_metadata.session_id", "thread": "client_metadata.thread_id", "turn": "client_metadata.turn_id", "window": "client_metadata.x-codex-window-id"}
	for field, path := range paths {
		value := strings.TrimSpace(gjson.GetBytes(raw, path).String())
		if len(value) <= 512 {
			result.Fields[field] = value
		}
	}
	return result
}

func codexIdentityFieldObservations(headers http.Header, body codexIdentityBodyObservation) map[string]CodexIdentityFieldObservation {
	metadata := headers.Get(openAIWSTurnMetadataHeader)
	values := map[string]string{"installation": gjson.Get(metadata, "installation_id").String(), "session": extractClientSessionID(headers), "thread": headers.Get("thread-id"), "turn": gjson.Get(metadata, "turn_id").String(), "window": headers.Get("x-codex-window-id")}
	result := make(map[string]CodexIdentityFieldObservation, len(values))
	for key, raw := range values {
		header := strings.TrimSpace(raw)
		value := body.Fields[key]
		observation := CodexIdentityFieldObservation{Consistency: "uninspected"}
		if header != "" {
			observation.HeaderDigest = codexRoutingDigest(header)[:16]
		}
		if value != "" {
			observation.BodyDigest = codexRoutingDigest(value)[:16]
		}
		if body.Inspected {
			switch {
			case header == "" || value == "":
				observation.Consistency = "missing"
			case header == value:
				observation.Consistency = "match"
			default:
				observation.Consistency = "mismatch"
			}
		}
		result[key] = observation
	}
	return result
}
