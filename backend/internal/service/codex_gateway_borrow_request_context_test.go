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

func TestCodexBorrowPinnedProbeKeepsBusinessTierOutOfQualification(t *testing.T) {
	account := borrowCoreAccount(2)
	var received []string
	s := newBorrowCoreTest(t, func(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		tier := gjson.GetBytes(borrowCoreBody(t, req), "service_tier").String()
		received = append(received, tier)
		assert.Equal(t, "model=gpt-6.1-sol", req.Header.Get(openAICodexRoutingHintHeader))
		return borrowCoreResponse("gpt-6.1-sol", "OK", "stable"), nil
	}, account)
	borrowCoreCandidate(s, time.Now().Add(time.Minute))
	_, req, _, err := s.accountTemplate(context.Background(), account.ID, "gpt-6.1-sol")
	require.NoError(t, err)
	for _, tier := range []string{"default", "auto", "scale", ""} {
		wire := req.WithContext(withCodexBorrowServiceTier(req.Context(), tier))
		_, applied, err := s.Apply(wire, account, "gpt-6.1-sol", "", nil, false)
		require.NoError(t, err)
		require.Empty(t, applied.Verification.ServiceTier)
	}
	require.Equal(t, []string{"", ""}, received)
	_, _, err = s.Apply(req.WithContext(withCodexBorrowServiceTier(req.Context(), "default")), account, "gpt-6.1-sol", "", nil, true)
	require.NoError(t, err, "the first tier's independent successful proof remains reusable")
	require.Len(t, received, 2)
}
