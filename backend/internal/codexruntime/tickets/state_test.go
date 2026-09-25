package tickets

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestRenewalStagesPersistAndStop(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	state := State{Schema: extensionv1.CodexRoutingSchema, Enrolled: true, Phase: "ready", NextAt: &now}
	if err := state.beginRouting(now, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	var restarted State
	if err := json.Unmarshal(raw, &restarted); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(restarted.beginRouting(now, false), ErrRunning) {
		t.Fatal("restarted running stage was replayed")
	}
	state.completeRouting(now, nil, extensionv1.CodexRoutingObservation{Code: "routing_incomplete"})
	if !errors.Is(state.beginRouting(now, false), ErrNotDue) {
		t.Fatal("retry ran before its persisted due time")
	}
	due := *state.NextAt
	if err := state.beginRouting(due, false); err != nil {
		t.Fatal(err)
	}
	state.completeRouting(due, nil, extensionv1.CodexRoutingObservation{Code: "routing_incomplete"})
	if state.Phase != "stopped" || state.NextAt != nil {
		t.Fatal("renewal did not stop after its second failure")
	}
	if !errors.Is(state.beginRouting(now.Add(2*time.Hour), false), ErrNotDue) {
		t.Fatal("stopped renewal restarted")
	}
}

func TestRoutingMigrationDiscardsEveryLegacyStateShape(t *testing.T) {
	for _, length := range []int{292, 312, 332, 356, 780} {
		for _, phase := range []string{"ready", "retry", "stopped", "manual_running"} {
			// Legacy timestamps are intentionally opaque: retired material must
			// neither reject the account nor become current routing evidence.
			raw, _ := json.Marshal(map[string]any{"phase": phase, "operation_id": "spent-job", "ticket": map[string]any{"state": strings.Repeat("x", length), "expires_at": "retired-format"}})
			var state State
			if err := json.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}
			state.migrateRouting()
			if state.Ticket != nil || state.Qualification != nil || state.ExpiresAt != nil || state.OperationID != "spent-job" {
				t.Fatalf("length %d: legacy material was not retired", length)
			}
			if phase == "stopped" || phase == "manual_running" {
				if state.Phase != phase || state.Enrolled || state.NextAt != nil {
					t.Fatalf("length %d: spent state was revived", length)
				}
			} else if state.Phase != "needs_cookie_verification" || !state.Enrolled {
				t.Fatalf("length %d: renewal intent was lost", length)
			}
		}
	}
}

func TestTicketEligibilityDoesNotDependOnBusinessStatus(t *testing.T) {
	if !Eligible("openai", "oauth", false) || !Eligible("openai", "setup-token", false) {
		t.Fatal("OAuth credentials excluded")
	}
	if Eligible("anthropic", "apikey", false) || Eligible("openai", "apikey", false) || Eligible("openai", "oauth", true) {
		t.Fatal("unsupported ticket account admitted")
	}
}
