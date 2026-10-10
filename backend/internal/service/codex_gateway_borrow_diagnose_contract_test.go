package service

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexBorrowProbeContractKeepsOneCandidateAndDoesNotQualifyBusiness(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(model, func(t *testing.T) {
			account := borrowCoreAccount(2)
			var bodies [][]byte
			var headers []http.Header
			s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				assert.EqualValues(t, 2, id)
				bodies = append(bodies, borrowCoreBody(t, req))
				headers = append(headers, req.Header.Clone())
				state := "minted-state"
				if req.Header.Get("X-Codex-Turn-State") != "" {
					state = "changed-state" // A failed verdict must not end the comparison.
				}
				return borrowCoreResponse(model, "OK", state, "__cflb=target-route; Secure; Path=/"), nil
			}, account)
			borrowCoreCandidate(s, time.Now().Add(time.Minute))
			var results []*CodexBorrowDiagnosticResult
			var used int32
			err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{Scenario: "probe_contract", AccountID: 2, Model: model, Transport: "http", ServiceTier: "priority"}, func(e CodexBorrowDiagnosticEvent) {
				used = e.Requests
				if e.Result != nil {
					results = append(results, e.Result)
				}
			})
			require.NoError(t, err)
			require.Len(t, results, 3)
			require.EqualValues(t, 6, used)
			require.Len(t, bodies, 6)
			for i, result := range results {
				require.True(t, result.ProbeOnly)
				require.False(t, result.Dispatched)
				require.False(t, result.Applied)
				require.False(t, result.Completed)
				require.Equal(t, "target_state_changed", result.Verification.Reason)
				require.True(t, result.Verification.MintCompleted && result.Verification.ContinueCompleted)
				require.Equal(t, model == "gpt-6-astra", result.ProbeContext.OriginalAstraModel)
				require.Equal(t, results[0].ProbeContext.TemplateFingerprint, result.ProbeContext.TemplateFingerprint)
				require.Equal(t, results[0].ProbeContext.CookieFingerprint, result.ProbeContext.CookieFingerprint)
				require.Equal(t, model, gjson.GetBytes(bodies[i*2], "model").String())
				require.Equal(t, headers[0].Get("session-id"), headers[i*2].Get("session-id"))
				require.NotEqual(t, headers[i*2].Get("session_id"), headers[i*2+1].Get("session_id"))
				require.Equal(t, "minted-state", headers[i*2+1].Get("X-Codex-Turn-State"))
				require.Contains(t, headers[i*2].Get("Cookie"), "__oailb=synthetic-borrowed-cookie")
				require.Contains(t, headers[i*2+1].Get("Cookie"), "__cflb=target-route")
			}
			require.True(t, bytes.HasPrefix(bodies[2], []byte(`{"model":`)))
			require.Equal(t, "priority", gjson.GetBytes(bodies[2], "service_tier").String())
			require.JSONEq(t, string(bodies[0]), string(bodies[2]))
			require.NotEqual(t, string(bodies[0]), string(bodies[2]), "the first comparison changes field order only")
			require.True(t, bytes.HasPrefix(bodies[4], []byte(`{"model":`)))
			require.False(t, gjson.GetBytes(bodies[4], "service_tier").Exists())
			require.Empty(t, s.targets)
			require.Empty(t, s.qualifications)
			require.Empty(t, s.Status().RecentUsage, "probe-only results cannot replace real request evidence")
		})
	}
}

func TestCodexBorrowProbeContractRequestBudgetAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		account := borrowCoreAccount(2)
		var calls int
		ctx, cancel := context.WithCancel(context.Background())
		s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
			calls++
			if cancelled {
				cancel()
			}
			return borrowCoreResponse("gpt-6-astra", "OK", "state"), nil
		}, account)
		borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
		err := s.Diagnose(ctx, CodexBorrowDiagnosticRequest{Scenario: "probe_contract", AccountID: 2, Model: "gpt-6-astra", Transport: "http", RequestLimit: 1}, func(CodexBorrowDiagnosticEvent) {})
		cancel()
		require.Error(t, err)
		require.Equal(t, 1, calls)
		require.Empty(t, s.targets)
	}
}

func TestCodexBorrowProbeContractStopsAtFrozenCandidateExpiry(t *testing.T) {
	var sources, targets int
	var s *CodexGatewayBorrowService
	s = newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			sources++
			return borrowCoreResponse("gpt-6-astra", "OK", "source-state", "__oailb=short-candidate; Secure; Path=/; Max-Age=1"), nil
		}
		targets++
		deadline, ok := req.Context().Deadline()
		assert.True(t, ok)
		assert.True(t, deadline.Equal(s.candidate.expires), "a comparison cannot outlive its fixed cookie")
		<-req.Context().Done()
		return nil, req.Context().Err()
	}, borrowCoreAccount(1), borrowCoreAccount(2))
	err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{Scenario: "probe_contract", AccountID: 2, Model: "gpt-6-astra", Transport: "http"}, func(CodexBorrowDiagnosticEvent) {})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, sources)
	require.Equal(t, 1, targets)
	require.Empty(t, s.qualifications)
}

func TestCodexBorrowProbeContractRespectsTierPolicyBeforeModelCalls(t *testing.T) {
	for _, action := range []string{BetaPolicyActionBlock, BetaPolicyActionFilter} {
		t.Run(action, func(t *testing.T) {
			var calls int
			s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				assert.False(t, gjson.GetBytes(borrowCoreBody(t, req), "service_tier").Exists())
				return borrowCoreResponse("gpt-6-astra", "OK", "state"), nil
			}, borrowCoreAccount(2))
			policy := openAIFastFilterPriorityPolicy()
			policy.Rules[0].Action = action
			s.gateway.settingService = newOpenAIGatewayServiceWithSettings(t, policy).settingService
			if action == BetaPolicyActionFilter {
				borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			}
			err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{Scenario: "probe_contract", AccountID: 2, Model: "gpt-6-astra", Transport: "http", ServiceTier: "priority"}, func(e CodexBorrowDiagnosticEvent) {
				if e.Result != nil {
					assert.Empty(t, e.Result.ProbeContext.FinalTier)
				}
			})
			if action == BetaPolicyActionBlock {
				var blocked *OpenAIFastBlockedError
				require.ErrorAs(t, err, &blocked)
				require.Zero(t, calls)
			} else {
				require.NoError(t, err)
				require.Equal(t, 4, calls, "a filtered tier needs no redundant middle comparison")
			}
		})
	}
}
