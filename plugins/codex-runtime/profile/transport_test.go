package profile

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestTransportPolicyControlsOnlyUnencodedResponsesBodies(t *testing.T) {
	query := extensionv1.CodexTransportQuery{Method: "POST", Path: "/backend-api/codex/responses", ContentType: "application/json", BodyPresent: true}
	if !TransportPlan(query, true).Compress || TransportPlan(query, false).Compress {
		t.Fatal("compression flag not honored")
	}
	for _, change := range []func(*extensionv1.CodexTransportQuery){
		func(q *extensionv1.CodexTransportQuery) { q.Path += "/compact" },
		func(q *extensionv1.CodexTransportQuery) { q.Method = "GET" },
		func(q *extensionv1.CodexTransportQuery) { q.ContentEncoding = "gzip" },
		func(q *extensionv1.CodexTransportQuery) { q.ContentType = "multipart/form-data; boundary=fixture" },
		func(q *extensionv1.CodexTransportQuery) { q.BodyPresent = false },
	} {
		candidate := query
		change(&candidate)
		if TransportPlan(candidate, true).Compress {
			t.Fatalf("unexpected compression: %+v", candidate)
		}
	}
}
