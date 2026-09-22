package profile

import (
	"net/http"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TransportPlan(query extensionv1.CodexTransportQuery, enabled bool) extensionv1.CodexTransportPlan {
	plan := extensionv1.CodexTransportPlan{Enabled: enabled}
	plan.Compress = enabled && query.BodyPresent && query.Method == http.MethodPost &&
		strings.HasSuffix(strings.TrimSuffix(query.Path, "/"), "/codex/responses") &&
		strings.TrimSpace(query.ContentEncoding) == "" &&
		(query.ContentType == "" || strings.Contains(strings.ToLower(query.ContentType), "json"))
	return plan
}
