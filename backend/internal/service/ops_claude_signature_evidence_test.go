package service

import (
	"strings"
	"testing"
)

// Queuing drops older raw bodies, but must retain the facts explaining a
// signature recovery and must not confuse semantic status with actual HTTP.
func TestClaudeSignatureEvidenceSurvivesOpsQueue(t *testing.T) {
	diagnostic := &ClaudeSignatureRecoveryDiagnostic{
		Source: "sse_error", Outcome: "upstream_rejected", Attempts: 1,
		Inbound: ClaudeSignatureSnapshot{TotalCount: 1, Signatures: []ClaudeSignatureValueFingerprint{{
			Path: "/messages/0/content/0/signature", Length: 13, SHA256: strings.Repeat("a", 64),
		}}},
	}
	entry := &OpsInsertErrorLogInput{UpstreamErrors: []*OpsUpstreamErrorEvent{{
		Kind: "stream_error", UpstreamStatusCode: 400, UpstreamHTTPStatusCode: 200,
		Message: "Invalid signature in thinking block", Detail: "old raw error",
		SignatureRecovery: diagnostic,
	}}}
	for i := 0; i < opsUpstreamErrorsBodyWindow; i++ {
		entry.UpstreamErrors = append(entry.UpstreamErrors, &OpsUpstreamErrorEvent{
			Kind: "http_error", UpstreamStatusCode: 500, Message: "later failure",
		})
	}
	if err := SanitizeOpsUpstreamErrorsForQueue(entry); err != nil {
		t.Fatal(err)
	}
	diagnostic.Inbound.Signatures[0].SHA256 = "mutated after queueing"
	events, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	if err != nil {
		t.Fatal(err)
	}
	first := events[0]
	if first.UpstreamStatusCode != 400 || first.UpstreamHTTPStatusCode != 200 {
		t.Fatalf("semantic/HTTP status changed: %d/%d", first.UpstreamStatusCode, first.UpstreamHTTPStatusCode)
	}
	if first.Detail != "" {
		t.Fatal("older body must still be dropped by the existing queue window")
	}
	if first.SignatureRecovery == nil || first.SignatureRecovery.Inbound.Signatures[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatal("bounded recovery fingerprints must survive independent of raw bodies")
	}
	if last := events[len(events)-1]; last.UpstreamStatusCode != 500 || last.SignatureRecovery != nil {
		t.Fatal("recovery bookkeeping must not replace the terminal upstream failure")
	}
}
