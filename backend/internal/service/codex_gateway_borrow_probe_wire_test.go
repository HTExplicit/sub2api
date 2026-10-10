package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexBorrowProbesStayPlainWhileGenerationKeepsCompression(t *testing.T) {
	oldCompression := codexRequestZstd.Load()
	SetCodexRequestZstdEnabled(true)
	defer SetCodexRequestZstdEnabled(oldCompression)
	var sources, targets int
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 1 {
			sources++
			assert.Empty(t, req.Header.Get("Content-Encoding"), "the pinned source protocol sends plain JSON")
			return borrowCoreResponse("gpt-6-astra", "OK", "source", "__oailb=source-cookie; Secure; Path=/; Max-Age=230"), nil
		}
		targets++
		if targets <= 2 {
			assert.Empty(t, req.Header.Get("Content-Encoding"), "a body helper must not silently recompress the two-shot probe")
		} else {
			assert.Equal(t, "zstd", req.Header.Get("Content-Encoding"), "ordinary generation retains the enabled runtime policy")
		}
		return borrowCoreResponse("gpt-6.1-sol", "OK", "stable-state"), nil
	}, borrowCoreAccount(1), borrowCoreAccount(2))
	_, err := s.Verify(context.Background(), 2, "gpt-6.1-sol")
	require.NoError(t, err)
	result, err := s.GeneratePelican(context.Background(), 2, "gpt-6.1-sol", "low")
	require.NoError(t, err)
	require.Equal(t, "complete", result.Status)
	require.Equal(t, 1, sources)
	require.Equal(t, 3, targets)
	require.True(t, codexRequestZstd.Load(), "diagnostic protocol isolation cannot toggle the global setting")
	require.Equal(t, "identity", s.Status().Candidate.SourceRequestEncoding)
}

func TestCodexBorrowEncodingComparisonChangesOnlyWireEncoding(t *testing.T) {
	oldCompression := codexRequestZstd.Load()
	SetCodexRequestZstdEnabled(true)
	defer SetCodexRequestZstdEnabled(oldCompression)
	var encodings []string
	var bodies []string
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		encodings = append(encodings, req.Header.Get("Content-Encoding"))
		bodies = append(bodies, string(borrowCoreBody(t, req)))
		return borrowCoreResponse("gpt-6.1-sol", "OK", "stable"), nil
	}, borrowCoreAccount(2))
	borrowCoreCandidate(s, time.Now().Add(codexGatewayBorrowTTL))
	var results []*CodexBorrowDiagnosticResult
	var requests int32
	err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{Scenario: "probe_contract", Comparison: "encoding", AccountID: 2, Model: "gpt-6.1-sol", Transport: "http", ServiceTier: "priority"}, func(e CodexBorrowDiagnosticEvent) {
		requests = e.Requests
		if e.Result != nil {
			results = append(results, e.Result)
		}
	})
	require.NoError(t, err)
	require.EqualValues(t, 4, requests)
	require.Equal(t, []string{"", "", "zstd", "zstd"}, encodings)
	for _, body := range bodies {
		require.Equal(t, bodies[0], body)
	}
	require.Len(t, results, 2)
	require.Equal(t, results[0].ProbeContext.CookieFingerprint, results[1].ProbeContext.CookieFingerprint)
	require.Equal(t, results[0].ProbeContext.PlaintextBodyFingerprint, results[1].ProbeContext.PlaintextBodyFingerprint)
	require.Equal(t, "identity", results[0].Verification.MintRequestEncoding)
	require.Equal(t, "identity", results[0].Verification.ContinueRequestEncoding)
	require.Equal(t, "zstd", results[1].Verification.MintRequestEncoding)
	require.Equal(t, "zstd", results[1].Verification.ContinueRequestEncoding)
	require.True(t, codexRequestZstd.Load())
	require.Empty(t, s.qualifications)
}
