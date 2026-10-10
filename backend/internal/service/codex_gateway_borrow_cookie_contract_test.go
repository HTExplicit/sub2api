package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type borrowHeaderRejectedBody struct{ reads, closes int }

func (b *borrowHeaderRejectedBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.ErrUnexpectedEOF
}
func (b *borrowHeaderRejectedBody) Close() error { b.closes++; return nil }

// Fixed contract: ranxi fd1b5ee4 ProbeOpenAICodexStateRoute checks every
// replacement __oailb at response headers, before consuming a potentially
// stalled/error body. Cookie scope does not hide a target route replacement.
func TestCodexBorrowPinnedRouteRejectsAtHeaders(t *testing.T) {
	for _, cookie := range []string{
		"__oailb=changed; Path=/unrelated; Domain=other.invalid",
		"__oailb=synthetic-borrowed-cookie; Max-Age=230; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		"__oailb=synthetic-borrowed-cookie; Max-Age=0",
	} {
		body := &borrowHeaderRejectedBody{}
		calls := 0
		account := borrowCoreAccount(2)
		s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{cookie}}, Body: body}, nil
		}, account)
		borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
		_, req, _, err := s.accountTemplate(context.Background(), account.ID, "gpt-6-astra")
		require.NoError(t, err)
		_, applied, err := s.Apply(req, account, "gpt-6-astra", "", nil, false)
		require.Nil(t, applied)
		var failure *CodexGatewayBorrowFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "target_route_changed", failure.Reason)
		require.True(t, failure.Verification.RouteChanged)
		require.False(t, failure.Verification.MintCompleted)
		require.Zero(t, body.reads)
		require.Equal(t, 1, body.closes)
		require.Equal(t, 1, calls)
	}
}

func TestCodexBorrowPinnedContinuationUsesFirstUsableCflb(t *testing.T) {
	account := borrowCoreAccount(2)
	calls := 0
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		if calls == 1 {
			return borrowCoreResponse("gpt-6-astra", "OK", "state",
				"__cflb=deleted; Max-Age=0", "__cflb=first; Path=/other; Expires=Thu, 01 Jan 1970 00:00:00 GMT", "__cflb=later"), nil
		}
		require.Equal(t, "__cflb=first; __oailb=synthetic-borrowed-cookie", req.Header.Get("Cookie"))
		return borrowCoreResponse("gpt-6-astra", "OK", "state"), nil
	}, account)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), account.ID, "gpt-6-astra")
	require.NoError(t, err)
	_, applied, err := s.Apply(req, account, "gpt-6-astra", "", nil, false)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.Equal(t, 2, calls)
}

func TestCodexBorrowSourceRequiresSSEAndExplicitCookieLifetime(t *testing.T) {
	for _, test := range []struct{ contentType, cookie string }{
		{"application/json", "__oailb=value; Secure; Path=/; Max-Age=230"},
		{"text/event-stream", "__oailb=value; Secure; Path=/"},
	} {
		source := borrowCoreAccount(1)
		s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
			response := borrowCoreResponse("gpt-6-astra", "OK", "state", test.cookie)
			response.Header.Set("Content-Type", test.contentType)
			return response, nil
		}, source)
		candidate, _, err := s.acquireSource(context.Background(), 1)
		require.Error(t, err)
		require.Nil(t, candidate)
	}
}

func TestCodexBorrowSourceRefreshFailureKeepsLivePrimary(t *testing.T) {
	calls := 0
	s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		return nil, errors.New("synthetic refresh failure")
	}, borrowCoreAccount(1))
	borrowCoreCandidate(s, time.Now().Add(50*time.Second))
	primary := s.candidate
	backup := *primary
	backup.sourceID = 4
	backup.cookie.Value = "backup"
	s.storeCandidateLocked(&backup)
	require.Same(t, primary, s.currentCandidateLocked(), "a backup does not replace a live primary")
	require.NoError(t, s.prepareSource(context.Background(), s.revision, s.revisionCtx))
	require.Same(t, primary, s.currentCandidateLocked())
	require.Equal(t, 1, calls)
	require.True(t, s.prepareFailedUntil.IsZero())
	require.Equal(t, "candidate_retained", s.Status().Acquisition.Phase)
}

func TestCodexBorrowPinnedTargetRequiresOriginalCompletedStream(t *testing.T) {
	for _, kind := range []string{"valid", "unterminated", "late_error", "done_alias"} {
		t.Run(kind, func(t *testing.T) {
			account := borrowCoreAccount(2)
			s := newBorrowCoreTest(t, func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				body := borrowCoreSSE("gpt-6.1-sol", "OK")
				switch kind {
				case "unterminated":
					body = strings.TrimSuffix(body, "\n")
				case "late_error":
					body += "data: {\"type\":\"error\",\"error\":{\"message\":\"retained failure\"}}\n\n"
				case "done_alias":
					body = strings.ReplaceAll(body, "response.completed", "response.done")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Turn-State": []string{"state"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}, account)
			borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
			_, req, _, err := s.accountTemplate(context.Background(), account.ID, "gpt-6.1-sol")
			require.NoError(t, err)
			_, applied, err := s.Apply(req, account, "gpt-6.1-sol", "", nil, false)
			if kind == "valid" {
				require.NoError(t, err)
				require.True(t, applied.Applied)
			} else {
				require.Error(t, err)
				require.Nil(t, applied)
			}
		})
	}
}

type borrowProbeWaitForEOF struct {
	io.Reader
	ctx context.Context
}

func (b *borrowProbeWaitForEOF) Read(data []byte) (int, error) {
	n, err := b.Reader.Read(data)
	if err == io.EOF {
		<-b.ctx.Done()
		return n, b.ctx.Err()
	}
	return n, err
}
func (b *borrowProbeWaitForEOF) Close() error { return nil }
func TestCodexBorrowPinnedTargetDoesNotCertifyBeforeEOF(t *testing.T) {
	account := borrowCoreAccount(2)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Turn-State": []string{"state"}}, Body: &borrowProbeWaitForEOF{Reader: strings.NewReader(borrowCoreSSE("gpt-6-astra", "OK")), ctx: req.Context()}}, nil
	}, account)
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	_, req, _, err := s.accountTemplate(context.Background(), account.ID, "gpt-6-astra")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := s.probeTarget(ctx, req, account, "gpt-6-astra", "", nil, s.candidate)
	require.False(t, result.Success)
	require.False(t, result.MintCompleted)
	require.Zero(t, result.ContinueStatus)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
