package service

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cindyGroupReaderStub struct {
	accounts []Account
	err      error
}

type mutableCindyGroupReaderStub struct {
	accounts []Account
	err      error
	calls    atomic.Int64
}

func (s *mutableCindyGroupReaderStub) ListByGroup(context.Context, int64) ([]Account, error) {
	s.calls.Add(1)
	return append([]Account(nil), s.accounts...), s.err
}

func (s *mutableCindyGroupReaderStub) ListCindyGroupIdentityMembers(ctx context.Context, groupID int64) ([]Account, error) {
	return s.ListByGroup(ctx, groupID)
}

func (s *mutableCindyGroupReaderStub) CindyGroupIdentityReaderMarker() {}

func (s cindyGroupReaderStub) ListByGroup(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), s.accounts...), s.err
}

func (s cindyGroupReaderStub) ListCindyGroupIdentityMembers(ctx context.Context, groupID int64) ([]Account, error) {
	return s.ListByGroup(ctx, groupID)
}

func (s cindyGroupReaderStub) CindyGroupIdentityReaderMarker() {}

type unmarkedCindyGroupReaderStub struct {
	called *atomic.Bool
}

func (s unmarkedCindyGroupReaderStub) ListByGroup(context.Context, int64) ([]Account, error) {
	s.called.Store(true)
	panic("unmarked group reader must not be called")
}

func TestIsStrictCindyGroupRequiresEveryNonDeletedMember(t *testing.T) {
	t.Parallel()
	groupID := int64(42)
	cindy := Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	other := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://api.openai.com"}}

	require.True(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{cindy, cindy}}, &groupID))
	exhausted := cindy
	exhausted.Schedulable = false
	now := time.Now()
	exhausted.CindyBalanceInsufficientAt = &now
	require.True(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{exhausted}}, &groupID), "persistent group identity must survive a fully exhausted pool")
	require.False(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{cindy, other}}, &groupID))
	disabledOther := other
	disabledOther.Status = StatusDisabled
	disabledOther.Schedulable = false
	require.False(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{cindy, disabledOther}}, &groupID))
	require.False(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{}, &groupID))
	var typedNil *cindyGroupReaderStub
	require.False(t, isStrictCindyGroup(context.Background(), typedNil, &groupID))
	var unmarkedCalled atomic.Bool
	require.False(t, isStrictCindyGroup(context.Background(), unmarkedCindyGroupReaderStub{called: &unmarkedCalled}, &groupID))
	require.False(t, unmarkedCalled.Load(), "unmarked repositories must not be queried")
}

func TestStrictCindyGroupIdentityIsUncachedAndPropagatesErrors(t *testing.T) {
	t.Parallel()
	groupID := int64(43)
	cindy := Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	repo := &mutableCindyGroupReaderStub{accounts: []Account{cindy}}

	strict, err := classifyStrictCindyGroup(context.Background(), repo, &groupID)
	require.NoError(t, err)
	require.True(t, strict)

	repo.accounts = []Account{{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}}
	strict, err = classifyStrictCindyGroup(context.Background(), repo, &groupID)
	require.NoError(t, err)
	require.False(t, strict, "the next request must observe an identity change without a TTL window")
	require.Equal(t, int64(2), repo.calls.Load())

	repo.err = context.DeadlineExceeded
	strict, err = classifyStrictCindyGroup(context.Background(), repo, &groupID)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, strict)
}

func TestCindyAnthropicAuthAlwaysUsesBearer(t *testing.T) {
	t.Parallel()
	account := &Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	header := make(http.Header)
	setAnthropicAPIKeyAuthHeader(header, account, "upstream-secret")
	require.Equal(t, "Bearer upstream-secret", header.Get("Authorization"))
	require.Empty(t, header.Get("x-api-key"))
}
