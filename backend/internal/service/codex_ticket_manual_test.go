package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// An in-memory durable-store adapter isolates gateway IO tests. SQL atomicity
// and claims are covered separately by repository integration tests.
type codexTicketMemoryStore struct {
	AccountRepository
	mu           sync.Mutex
	account      Account
	states       map[string]*CodexTicketLifecycle
	updates      map[string]any
	persistError error
}

func newCodexTicketMemoryStore(a *Account) *codexTicketMemoryStore {
	return &codexTicketMemoryStore{account: *a, states: map[string]*CodexTicketLifecycle{}, updates: map[string]any{}}
}
func (r *codexTicketMemoryStore) GetByID(context.Context, int64) (*Account, error) {
	a := r.account
	return &a, nil
}
func (r *codexTicketMemoryStore) SeedCodexTicketRenewals(context.Context, []string, int, time.Time) error {
	return nil
}
func (r *codexTicketMemoryStore) DueCodexTickets(_ context.Context, _ []string, now time.Time, _ int) ([]CodexTicketLifecycle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []CodexTicketLifecycle
	for _, s := range r.states {
		if (s.Phase == "ready" || s.Phase == "retry") && s.NextAt != nil && !now.Before(*s.NextAt) {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (r *codexTicketMemoryStore) ClaimCodexTicket(_ context.Context, id int64, model, operation string, jobID int64, manual, force bool, now time.Time) (*CodexTicketLifecycle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[model]
	if s == nil {
		s = &CodexTicketLifecycle{AccountID: id, Model: model, Identity: CodexTicketAccountIdentity(&r.account)}
		r.states[model] = s
	}
	if s.LeaseID != "" {
		return nil, ErrCodexTicketBusy
	}
	if s.Operation == operation && manual {
		return nil, ErrCodexTicketAlreadyAttempted
	}
	if manual && !force && s.ExpiresAt != nil && now.Before(*s.ExpiresAt) {
		return nil, ErrCodexTicketValid
	}
	if err := s.Begin(now, manual); err != nil {
		return nil, err
	}
	s.LeaseID = operation
	s.Operation = operation
	s.JobID = jobID
	copy := *s
	return &copy, nil
}
func (r *codexTicketMemoryStore) FinishCodexTicket(_ context.Context, claim *CodexTicketLifecycle, ticket *CodexTicketRecord, result CodexTicketResult, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.persistError != nil {
		return false, r.persistError
	}
	s := r.states[claim.Model]
	if s.LeaseID != claim.LeaseID {
		return false, nil
	}
	s.Complete(now, ticket, result)
	if ticket != nil {
		r.updates[claim.Model] = ticket
	}
	return ticket != nil, nil
}
func (r *codexTicketMemoryStore) StopCodexTicket(_ context.Context, _ int64, models []string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range models {
		if s := r.states[m]; s != nil {
			s.Phase = "stopped"
			s.Complete(now, nil, CodexTicketFailure("ticket_stopped"))
		}
	}
	return nil
}

func TestCodexTicketFiniteRenewalSchedule(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	s := &CodexTicketLifecycle{}
	success := CodexTicketResult{Success: true}
	ticket := &CodexTicketRecord{ExpiresAt: expiry}
	require.NoError(t, s.Begin(now, true))
	s.Complete(now, ticket, success)
	require.Equal(t, expiry.Add(-time.Minute), *s.NextAt)
	require.ErrorIs(t, s.Begin(expiry.Add(-61*time.Second), false), ErrCodexTicketNotDue)
	require.NoError(t, s.Begin(expiry.Add(-time.Minute), false))
	require.Equal(t, "pre_running", s.Phase)
	s.Complete(expiry.Add(-50*time.Second), nil, CodexTicketFailure("ticket_length"))
	require.Equal(t, "retry", s.Phase)
	require.Equal(t, expiry.Add(time.Minute), *s.NextAt)
	require.ErrorIs(t, s.Begin(expiry, false), ErrCodexTicketNotDue)
	require.NoError(t, s.Begin(expiry.Add(time.Minute), false))
	s.Complete(expiry.Add(61*time.Second), nil, CodexTicketFailure("ticket_timeout"))
	require.Equal(t, "stopped", s.Phase)
	require.Nil(t, s.NextAt)
	require.ErrorIs(t, s.Begin(expiry.Add(24*time.Hour), false), ErrCodexTicketNotDue)
	// Manual success is the only transition out of stopped.
	require.NoError(t, s.Begin(expiry.Add(time.Hour), true))
	ticket.ExpiresAt = expiry.Add(2 * time.Hour)
	s.Complete(expiry.Add(time.Hour), ticket, success)
	require.Equal(t, "ready", s.Phase)
	require.Equal(t, ticket.ExpiresAt.Add(-time.Minute), *s.NextAt)
}

func TestCodexTicketMissedWindowAndInterruptedStage(t *testing.T) {
	now := time.Now()
	expiry := now.Add(-30 * time.Second)
	due := expiry.Add(-time.Minute)
	s := &CodexTicketLifecycle{Phase: "ready", ExpiresAt: &expiry, NextAt: &due}
	require.ErrorIs(t, s.Begin(now, false), ErrCodexTicketNotDue)
	require.Equal(t, "retry", s.Phase)
	require.NoError(t, s.Begin(now.Add(time.Hour), false))
	require.Equal(t, "post_running", s.Phase)
	s.Complete(now.Add(time.Hour), nil, CodexTicketFailure("ticket_interrupted"))
	require.Equal(t, "stopped", s.Phase)
	expiry = now.Add(time.Hour)
	due = expiry.Add(-time.Minute)
	s = &CodexTicketLifecycle{Phase: "ready", ExpiresAt: &expiry, NextAt: &due}
	require.NoError(t, s.Begin(now, true))
	s.Complete(now, nil, CodexTicketFailure("ticket_length"))
	require.Equal(t, "ready", s.Phase)
	require.Equal(t, due, *s.NextAt)
	s = &CodexTicketLifecycle{}
	require.NoError(t, s.Begin(now, true))
	s.Complete(now, nil, CodexTicketFailure("ticket_length"))
	require.Equal(t, "stopped", s.Phase)
}

func TestCodexTicketManualOnlyOneRequestAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		length  int
		persist bool
		success bool
	}{{"success", 292, false, true}, {"312", 312, false, false}, {"database failure", 292, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			a := ticketTestAccount(41)
			a.Status = StatusActive
			r := newCodexTicketMemoryStore(a)
			if tc.persist {
				r.persistError = errors.New("database unavailable")
			}
			var calls atomic.Int64
			upstream := &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				resp := codexTicketResponse()
				resp.Header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(tc.length))
				return resp, nil
			}}
			s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example.com:8080"}, upstream)
			s.accountRepo = r
			s.refreshOpenAICodexTickets(context.Background())
			require.Zero(t, calls.Load(), "never probe unenrolled accounts")
			result := s.HarvestCodexTicket(context.Background(), a.ID, "gpt-6-astra", "manual1", 0, false)
			require.Equal(t, tc.success, result.Success)
			require.Equal(t, int64(1), calls.Load())
			require.Nil(t, r.states["gpt-5.6-sol"], "Astra success cannot enroll Sol")
			if tc.success {
				require.NotNil(t, s.lookupOpenAICodexTicket(a, "gpt-6-astra"))
			} else {
				require.Nil(t, s.lookupOpenAICodexTicket(a, "gpt-6-astra"))
			}
			s.refreshOpenAICodexTickets(context.Background())
			require.Equal(t, int64(1), calls.Load())
		})
	}
}

func TestCodexTicketEligibilityIndependentOfAccountStatus(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, status := range []string{StatusActive, "disabled", "error"} {
			t.Run(accountType+"/"+status, func(t *testing.T) {
				a := ticketTestAccount(41)
				a.Type, a.Status, a.Schedulable, a.ErrorMessage = accountType, status, false, "preserve diagnostic"
				r := newCodexTicketMemoryStore(a)
				var calls atomic.Int64
				upstream := &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					resp := codexTicketResponse()
					resp.Header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
					return resp, nil
				}}
				s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example.com:8080"}, upstream)
				s.accountRepo = r
				result := s.HarvestCodexTicket(context.Background(), a.ID, "gpt-6-astra", "manual", 0, false)
				require.True(t, result.Success)
				require.Equal(t, int64(1), calls.Load())
				require.Equal(t, "ready", r.states["gpt-6-astra"].Phase)
				require.Equal(t, status, a.Status)
				require.False(t, a.Schedulable)
				require.Equal(t, "preserve diagnostic", a.ErrorMessage)
			})
		}
	}
	for _, a := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: func() *int64 { id := int64(1); return &id }()},
		nil,
	} {
		require.False(t, CodexTicketAccountEligible(a))
	}
}

type ticketTokenFailureRepo struct {
	*codexTicketMemoryStore
	statusWrites int
}

func (r *ticketTokenFailureRepo) SetError(context.Context, int64, string) error {
	r.statusWrites++
	return nil
}

func TestCodexTicketExpiredCredentialFailurePreservesAccountStatus(t *testing.T) {
	a := ticketTestAccount(41)
	a.Status, a.Schedulable, a.ErrorMessage = "disabled", false, "original diagnostic"
	a.Credentials["expires_at"] = time.Now().Add(-time.Minute).Format(time.RFC3339)
	delete(a.Credentials, "refresh_token")
	r := &ticketTokenFailureRepo{codexTicketMemoryStore: newCodexTicketMemoryStore(a)}
	provider := NewOpenAITokenProvider(r, nil, nil)
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example.com:8080"}, &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid credentials must not reach the upstream")
		return nil, nil
	}})
	s.accountRepo, s.openAITokenProvider = r, provider
	result := s.HarvestCodexTicket(context.Background(), a.ID, "gpt-6-astra", "manual", 0, false)
	require.False(t, result.Success)
	require.Equal(t, "ticket_token", result.Code)
	require.Zero(t, r.statusWrites)
	require.Equal(t, "disabled", r.account.Status)
	require.False(t, r.account.Schedulable)
	require.Equal(t, "original diagnostic", r.account.ErrorMessage)
	_, err := provider.GetAccessToken(context.Background(), a)
	require.Error(t, err)
	require.Equal(t, 1, r.statusWrites, "ordinary inference still quarantines unusable credentials")
}
