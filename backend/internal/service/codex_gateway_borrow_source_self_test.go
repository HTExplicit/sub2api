package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexBorrowSourceSelfCheckUsesOnlyCurrentOwner(t *testing.T) {
	var calls int
	source := borrowCoreAccount(1)
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		assert.EqualValues(t, 1, id)
		assert.Equal(t, codexGatewayBorrowSourceModel, gjson.GetBytes(borrowCoreBody(t, req), "model").String())
		assert.Empty(t, req.Header.Get("Content-Encoding"))
		return borrowCoreResponse(codexGatewayBorrowSourceModel, "OK", "source-state"), nil
	}, source)
	borrowCoreCandidate(s, time.Now().Add(time.Minute))
	var results []*CodexBorrowDiagnosticResult
	err := s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{Scenario: "probe_contract", AccountID: 1, Model: codexGatewayBorrowSourceModel, Transport: "http"}, func(e CodexBorrowDiagnosticEvent) {
		if e.Result != nil {
			results = append(results, e.Result)
		}
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	for _, r := range results {
		require.Equal(t, "source", r.ProbeContext.SubjectRole)
		require.True(t, r.ProbeOnly)
		require.False(t, r.Applied || r.Dispatched || r.Completed)
	}
	require.Empty(t, s.qualifications)
	require.Empty(t, s.Status().RecentUsage)
	for _, request := range []CodexBorrowDiagnosticRequest{
		{AccountID: 1, Model: codexGatewayBorrowSourceModel, Transport: "http", Mode: "borrowed"},
		{Scenario: "probe_contract", AccountID: 1, Model: "gpt-6.1-sol", Transport: "http"},
	} {
		require.Error(t, s.Diagnose(context.Background(), request, func(CodexBorrowDiagnosticEvent) {}))
	}
	require.Equal(t, 2, calls, "source selection cannot enter generation or another model")
	s.candidate = nil
	require.Error(t, s.Diagnose(context.Background(), CodexBorrowDiagnosticRequest{Scenario: "probe_contract", AccountID: 1, Model: codexGatewayBorrowSourceModel, Transport: "http"}, func(CodexBorrowDiagnosticEvent) {}))
	require.Equal(t, 2, calls, "a self-check does not silently prepare a replacement candidate")
}
