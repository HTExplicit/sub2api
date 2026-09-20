package recovery

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestRecoveryPolicyRequiresExactErrorAndUnambiguousLocalCipherScope(t *testing.T) {
	code := "invalid_encrypted_content"
	envelope := extensionv1.RecoveryEnvelope{ValidJSON: true, ErrorLocation: "error", Code: &code, ParamValid: true}
	if !Rejection(envelope).Recognized {
		t.Fatal("exact rejection not recognized")
	}
	envelope.Statuses = []int{429}
	if Rejection(envelope).Recognized {
		t.Fatal("protected quota error accepted")
	}
	query := extensionv1.RecoverySelectionQuery{BodyValid: true, ToolHistoryValid: true, CipherIndices: []int{1, 3}, EncryptedFields: 2}
	if len(Select(query).Indices) != 2 {
		t.Fatal("unambiguous local set was lost")
	}
	query.ServerContext = true
	if Select(query).Reason != "server_held_context" {
		t.Fatal("server-owned context incorrectly changed")
	}
	query.Param = "input[3].encrypted_content"
	if result := Select(query); len(result.Indices) != 1 || result.Indices[0] != 3 {
		t.Fatal("explicit target changed")
	}
	query.Param = "input[2].encrypted_content"
	if Select(query).Reason != "target_not_reasoning_ciphertext" {
		t.Fatal("unrelated item selected")
	}
	query.Param = "input"
	query.ServerContext = false
	query.EncryptedFields = 3
	if Select(query).Reason != "ambiguous_encrypted_carriers" {
		t.Fatal("compaction ciphertext may be affected")
	}
}
