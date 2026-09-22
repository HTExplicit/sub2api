package tickets

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRenewalStagesPersistAndStop(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	ticket := &Ticket{State: "gAAAAA" + strings.Repeat("a", 286), AccountID: 1, Identity: "subject", Model: "astra", CapturedAt: now, ExpiresAt: now.Add(TTL)}
	state := State{}
	if err := state.Begin(now, true); err != nil {
		t.Fatal(err)
	}
	state.Complete(now, ticket, "ticket_ready")
	if !ticket.Valid(now, 1, "subject", "astra") || ticket.Valid(now, 1, "different", "astra") {
		t.Fatal("ticket ownership mismatch")
	}
	if err := state.Begin(now.Add(59*time.Minute), false); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	var restarted State
	if err := json.Unmarshal(raw, &restarted); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(restarted.Begin(now.Add(59*time.Minute), false), ErrRunning) {
		t.Fatal("restarted running stage was replayed")
	}
	state.Complete(now.Add(59*time.Minute), nil, "ticket_length")
	if !errors.Is(state.Begin(now.Add(time.Hour), false), ErrNotDue) {
		t.Fatal("post expiry retry ran early")
	}
	if err := state.Begin(now.Add(61*time.Minute), false); err != nil {
		t.Fatal(err)
	}
	state.Complete(now.Add(61*time.Minute), nil, "ticket_length")
	if state.Phase != "stopped" || state.NextAt != nil {
		t.Fatal("renewal did not stop after its second failure")
	}
	if !errors.Is(state.Begin(now.Add(2*time.Hour), false), ErrNotDue) {
		t.Fatal("stopped renewal restarted")
	}
}

func TestTicketEligibilityDoesNotDependOnBusinessStatus(t *testing.T) {
	if !Eligible("openai", "oauth", false) || !Eligible("openai", "setup-token", false) {
		t.Fatal("OAuth credentials excluded")
	}
	if Eligible("cindy", "apikey", false) || Eligible("openai", "apikey", false) || Eligible("openai", "oauth", true) {
		t.Fatal("unsupported ticket account admitted")
	}
}
