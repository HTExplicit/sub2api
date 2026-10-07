package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type borrowHTTPMutationRepo struct {
	*borrowCoreAccounts
	writes atomic.Int32
}

func (r *borrowHTTPMutationRepo) SetError(context.Context, int64, string) error {
	r.writes.Add(1)
	return nil
}
func (r *borrowHTTPMutationRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.writes.Add(1)
	return nil
}
func (r *borrowHTTPMutationRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.writes.Add(1)
	return nil
}
func (r *borrowHTTPMutationRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.writes.Add(1)
	return nil
}

func TestCodexGatewayBorrowHTTPPreparationExcludedFromFirstOutputBudgets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var probeCalls, businessCalls atomic.Int32
	probe := borrowCoreProbe(func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if probeCalls.Add(1) == 1 {
			// Longer than the entire business first-output budget. This local
			// observation is bound by the original client, not that business guard.
			timer := time.NewTimer(1200 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-timer.C:
			}
			return borrowCoreResponse("gpt-6-astra", "", "synthetic-state"), nil
		}
		return borrowCoreResponse("gpt-6-astra", "", ""), nil
	})
	account := borrowCoreAccount(2)
	account.Schedulable = true
	borrow := newBorrowCoreTest(t, probe, account)
	borrowCoreCandidate(borrow, time.Now().Add(time.Minute))
	repo := &borrowHTTPMutationRepo{borrowCoreAccounts: &borrowCoreAccounts{rows: map[int64]*Account{2: account}}}
	borrow.accounts, borrow.gateway.accountRepo = repo, repo
	svc := borrow.gateway
	svc.cfg = &config.Config{Gateway: config.GatewayConfig{OpenAIFirstOutputTimeoutSeconds: 1, MaxLineSize: defaultMaxLineSize}}
	svc.rateLimitService = &RateLimitService{accountRepo: repo}
	svc.httpUpstream = borrowCoreProbe(func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		businessCalls.Add(1)
		// Exercise both response-header and semantic-output waiting, after
		// preparation already exceeded the original one-second deadline.
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-timer.C:
		}
		reader, writer := io.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			select {
			case <-req.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n"+borrowCoreSSE("gpt-6-astra", "OK"))
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
	})
	body := []byte(`{"model":"gpt-6-astra","stream":true,"reasoning":{"effort":"low"},"input":"hello"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	clientCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(clientCtx)
	started := time.Now()
	result, err := svc.Forward(clientCtx, c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int32(2), probeCalls.Load())
	require.Equal(t, int32(1), businessCalls.Load())
	require.Contains(t, recorder.Body.String(), "OK")
	require.GreaterOrEqual(t, result.Duration, 1200*time.Millisecond)
	require.NotNil(t, result.FirstTokenMs)
	require.GreaterOrEqual(t, *result.FirstTokenMs, 1200, "TTFT must retain preparation wall time")
	require.GreaterOrEqual(t, time.Since(started), 1200*time.Millisecond)
	require.Zero(t, repo.writes.Load())
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-astra"))
}

func TestCodexGatewayBorrowHTTPRecoveryPreservesCallerContext(t *testing.T) {
	for _, lifecycle := range []string{"cancel", "deadline"} {
		for _, replacedGuard := range []bool{false, true} {
			name := lifecycle + "/prepared_context"
			if replacedGuard {
				name = lifecycle + "/replaced_guard"
			}
			t.Run(name, func(t *testing.T) {
				type callerValueKey struct{}
				values := context.WithValue(context.Background(), callerValueKey{}, "retained")
				caller, cancel := context.WithCancel(values)
				if lifecycle == "deadline" {
					cancel()
					caller, cancel = context.WithTimeout(values, 250*time.Millisecond)
				}
				defer cancel()
				transport, _ := detachUpstreamContext(caller)
				transport = WithHTTPUpstreamProfile(transport, HTTPUpstreamProfileOpenAI)
				var oldGuard *openAIFirstOutputHeaderGuard
				if replacedGuard {
					transport, oldGuard = newOpenAIFirstOutputHeaderGuard(transport, func() {}, time.Now().Add(time.Minute))
					defer oldGuard.close()
				}
				account := borrowCoreAccount(2)
				var probes, businessCalls atomic.Int32
				borrow := newBorrowCoreTest(t, borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
					if probes.Add(1) == 1 {
						time.Sleep(10 * time.Millisecond)
						return borrowCoreResponse("gpt-6-astra", "", "synthetic-state"), nil
					}
					return borrowCoreResponse("gpt-6-astra", "", ""), nil
				}), account)
				borrowCoreCandidate(borrow, time.Now().Add(time.Minute))
				body := []byte(`{"model":"gpt-6-astra","input":"synthetic recovery"}`)
				req, err := http.NewRequestWithContext(transport, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer synthetic-target")
				recovery := &openAIReasoningRecoveryState{ctx: caller, account: account, token: "synthetic-target", enabled: true, retryUsed: true, retryBody: body}
				recovery.identity = openAIReasoningRequestIdentity(account, req, "", recovery.token)
				defer recovery.Close()
				req, _, err = recovery.PrepareRequest(req, body, "")
				require.NoError(t, err)
				prepared, elapsed, err := borrow.gateway.prepareCodexGatewayBorrowHTTP(caller, req, account, "gpt-6-astra", "")
				require.NoError(t, err)
				require.Equal(t, int32(2), probes.Load(), "fixture must qualify the recovery request")
				require.Positive(t, elapsed)
				if oldGuard != nil {
					oldGuard.close()
				}
				adjusted := codexGatewayBorrowHTTPPreparedContext(caller, prepared.Context(), replacedGuard, recovery.RecoveryAttempt())
				if !replacedGuard {
					require.Same(t, prepared.Context(), adjusted)
				}
				callerDeadline, callerHasDeadline := caller.Deadline()
				deadline, hasDeadline := adjusted.Deadline()
				require.Equal(t, callerHasDeadline, hasDeadline)
				require.Equal(t, callerDeadline, deadline)
				require.Equal(t, "retained", adjusted.Value(callerValueKey{}))
				require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(adjusted))
				require.True(t, HTTPUpstreamRedirectsDisabled(adjusted))
				require.NotNil(t, adjusted.Value(codexGatewayBorrowHTTPPreparationContextKey{}))
				require.NoError(t, adjusted.Err())
				require.NoError(t, context.Cause(adjusted), "replaced header guard must not supply the cancellation cause")
				prepared = prepared.WithContext(adjusted)
				if lifecycle == "cancel" {
					cancel()
				}
				select {
				case <-adjusted.Done():
				case <-time.After(time.Second):
					t.Fatal("qualified recovery lost the caller's lifecycle")
				}
				borrow.gateway.httpUpstream = borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
					businessCalls.Add(1)
					return nil, errors.New("expired recovery must not dispatch")
				})
				_, err = borrow.gateway.doOpenAICodexUpstream(prepared, account, "", "gpt-6-astra")
				require.ErrorIs(t, err, adjusted.Err())
				require.Equal(t, int32(2), probes.Load())
				require.Zero(t, businessCalls.Load())
			})
		}
	}
}

func TestCodexGatewayBorrowHTTPFailureUsesAccountNeutralFailover(t *testing.T) {
	for _, cause := range []error{errors.New("source proxy connection refused: complete diagnostic"), ErrCodexGatewayBorrowBusy, ErrCodexGatewayBorrowUnavailable, ErrCodexGatewayBorrowChanged} {
		t.Run(cause.Error(), func(t *testing.T) {
			account := borrowCoreAccount(2)
			repo := &borrowHTTPMutationRepo{borrowCoreAccounts: &borrowCoreAccounts{}}
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, &CodexGatewayBorrowFailure{Cause: cause}, false)
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, GatewayFailureScopeRequest, failure.Scope)
			require.Equal(t, CodexGatewayBorrowPreparationFailureReason, failure.Reason)
			require.True(t, failure.SuppressAccountHealthPenalty)
			require.True(t, failure.ShouldRetryNextAccount())
			require.False(t, failure.ShouldReportAccountScheduleFailure())
			// This is the same exhaustion owner used by the handler after its
			// narrow no-same-account-retry policy selects another account.
			svc.CooldownOpenAIRetryExhausted(context.Background(), account, "gpt-6-astra", failure)
			require.Zero(t, repo.writes.Load())
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-astra"))
			value, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events, ok := value.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, events, 1)
			require.Contains(t, events[0].Detail, cause.Error())
		})
	}
	ordinary := (&OpenAIGatewayService{}).handleOpenAIUpstreamTransportError(context.Background(), nil, borrowCoreAccount(2), errors.New("connection refused"), false)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, ordinary, &failure)
	require.Equal(t, OpenAIPersistentTransportFailureReason, failure.Reason)
	require.False(t, failure.SuppressAccountHealthPenalty)
}

func TestCodexGatewayBorrowHTTPPreparedDispatchNeverReprepares(t *testing.T) {
	for _, change := range []string{"expired", "configuration", "identity"} {
		t.Run(change, func(t *testing.T) {
			var probeCalls, businessCalls atomic.Int32
			probe := borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				if probeCalls.Add(1) == 1 {
					return borrowCoreResponse("gpt-6-astra", "", "synthetic-state"), nil
				}
				return borrowCoreResponse("gpt-6-astra", "", ""), nil
			})
			account := borrowCoreAccount(2)
			borrow := newBorrowCoreTest(t, probe, account)
			borrowCoreCandidate(borrow, time.Now().Add(time.Minute))
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, chatgptCodexURL, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer synthetic-target")
			prepared, _, err := borrow.gateway.prepareCodexGatewayBorrowHTTP(context.Background(), req, account, "gpt-6-astra", "")
			require.NoError(t, err)
			switch change {
			case "expired":
				borrow.mu.Lock()
				borrow.candidate.expires = time.Now().Add(-time.Second)
				borrow.mu.Unlock()
			case "configuration":
				cfg := borrow.ConfigSnapshot()
				cfg.Enabled = false
				borrow.publishConfig(cfg, false)
			case "identity":
				prepared.Header.Set("Authorization", "Bearer changed-target")
			}
			borrow.gateway.httpUpstream = borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				businessCalls.Add(1)
				return nil, errors.New("must not dispatch on changed evidence")
			})
			_, err = borrow.gateway.doOpenAICodexUpstream(prepared, account, "", "gpt-6-astra")
			require.True(t, IsCodexGatewayBorrowFailure(err), "changed evidence must fail locally")
			require.Equal(t, int32(2), probeCalls.Load(), "the guarded dispatch may only check cached evidence")
			require.Zero(t, businessCalls.Load())
		})
	}
}

func TestCodexGatewayBorrowHTTPPreparedDispatchPreservesFrozenRequest(t *testing.T) {
	for _, changedCookie := range []bool{false, true} {
		name := "canonical_cookie"
		if changedCookie {
			name = "changed_cookie"
		}
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"synthetic-cipher"}]}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			account := borrowCoreAccount(2)
			var probes, businessCalls atomic.Int32
			borrow := newBorrowCoreTest(t, borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				if probes.Add(1) == 1 {
					return borrowCoreResponse("gpt-6-astra", "", "synthetic-state"), nil
				}
				return borrowCoreResponse("gpt-6-astra", "", ""), nil
			}), account)
			borrowCoreCandidate(borrow, time.Now().Add(time.Minute))
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer synthetic-target")
			req.Header.Set("Cookie", "target=kept")
			recovery := &openAIReasoningRecoveryState{ctx: context.Background(), c: c, account: account, enabled: true}
			c.Set(openAIReasoningRecoveryContextKey, recovery)
			req, _, err = recovery.PrepareRequest(req, body, "")
			require.NoError(t, err)
			prepared, _, err := borrow.gateway.prepareCodexGatewayBorrowHTTP(context.Background(), req, account, "gpt-6-astra", "")
			require.NoError(t, err)
			guardCtx, guard := newOpenAIFirstOutputHeaderGuard(prepared.Context(), func() {}, time.Now().Add(time.Minute))
			defer guard.close()
			prepared = prepared.WithContext(guardCtx)
			if changedCookie {
				prepared.Header.Set("Cookie", "target=kept; __oailb=stale")
			}
			finalized, err := borrow.gateway.applyCodexGatewayBorrowHTTP(prepared, account, "gpt-6-astra", "")
			require.NoError(t, err)
			if changedCookie {
				require.NotSame(t, prepared, finalized)
			} else {
				require.Same(t, prepared, finalized)
			}
			recovery.BindDiagnosticRequest(body, finalized)
			borrow.gateway.httpUpstream = borrowCoreProbe(func(got *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				businessCalls.Add(1)
				require.Same(t, finalized, got, "cached dispatch must keep the diagnostic's plaintext request")
				require.Nil(t, got.GetBody, "diagnostics must not restore transparent POST replay")
				diagnostic := buildOpenAIContinuationDiagnostic(c, body, got, body, []byte(`{"error":{"message":"bad request"}}`), "unclassified_bad_request")
				require.NotNil(t, diagnostic)
				require.Equal(t, "frozen_request", diagnostic.Wire.BodySource)
				require.False(t, diagnostic.Wire.InspectionLimited)
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("synthetic rejection"))}, nil
			})
			oldCompression := codexRequestZstd.Load()
			SetCodexRequestZstdEnabled(false)
			defer SetCodexRequestZstdEnabled(oldCompression)
			resp, err := borrow.gateway.doOpenAICodexUpstream(finalized, account, "", "gpt-6-astra")
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.Equal(t, int32(2), probes.Load())
			require.Equal(t, int32(1), businessCalls.Load())
		})
	}
}

type borrowHTTPUnreadBody struct{ reads atomic.Int32 }

func (r *borrowHTTPUnreadBody) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, errors.New("business body must not be inspected by the borrow gate")
}
func (*borrowHTTPUnreadBody) Close() error { return nil }

func TestCodexGatewayBorrowHTTPOrdinaryGatingDoesNotInspectBody(t *testing.T) {
	for _, ordinary := range []string{"other_model", "other_host", "other_method", "other_path", "feature_off", "other_account", "nil_service"} {
		t.Run(ordinary, func(t *testing.T) {
			var probes, business atomic.Int32
			probe := borrowCoreProbe(func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				probes.Add(1)
				return nil, errors.New("ordinary request must not probe")
			})
			account := borrowCoreAccount(2)
			borrow := newBorrowCoreTest(t, probe, account)
			model := "gpt-6-astra"
			req, err := http.NewRequest(http.MethodPost, chatgptCodexURL, nil)
			require.NoError(t, err)
			switch ordinary {
			case "other_model":
				model = "gpt-5.5"
			case "other_host":
				req.URL.Host = "api.openai.com"
			case "other_method":
				req.Method = http.MethodGet
			case "other_path":
				req.URL.Path += "/compact"
			case "feature_off":
				cfg := borrow.ConfigSnapshot()
				cfg.Enabled = false
				borrow.publishConfig(cfg, false)
			case "other_account":
				account = borrowCoreAccount(9)
			case "nil_service":
				borrow.gateway.gatewayBorrow = nil
			}
			body := &borrowHTTPUnreadBody{}
			req.Body, req.ContentLength = body, 100<<20
			req.GetBody = func() (io.ReadCloser, error) { t.Fatal("borrow gate must not reread GetBody"); return nil, nil }
			prepared, elapsed, err := borrow.gateway.prepareCodexGatewayBorrowHTTP(context.Background(), req, account, model, "")
			require.NoError(t, err)
			require.Zero(t, elapsed)
			require.Same(t, req, prepared, "ordinary requests must retain their diagnostic identity")
			require.Nil(t, prepared.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}))
			require.Same(t, body, prepared.Body)
			borrow.gateway.httpUpstream = borrowCoreProbe(func(got *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				business.Add(1)
				require.Same(t, body, got.Body)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ordinary"))}, nil
			})
			oldCompression := codexRequestZstd.Load()
			SetCodexRequestZstdEnabled(false)
			defer SetCodexRequestZstdEnabled(oldCompression)
			resp, err := borrow.gateway.doOpenAICodexUpstream(prepared, account, "", model)
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.Zero(t, probes.Load())
			require.Zero(t, body.reads.Load())
			require.Equal(t, int32(1), business.Load())
		})
	}
}
