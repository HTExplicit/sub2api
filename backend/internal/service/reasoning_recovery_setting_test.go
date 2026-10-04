package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newReasoningRecoverySwitchForTest returns a switch with a fixed value and no
// store behind it.
func newReasoningRecoverySwitchForTest(enabled bool) *ReasoningRecoveryService {
	recovery := NewReasoningRecoveryService(nil)
	recovery.publish(ReasoningRecoveryConfig{Enabled: enabled})
	return recovery
}

type reasoningRecoverySettingStore struct {
	SettingRepository
	mu      sync.Mutex
	values  map[string]string
	reads   int
	readErr error
	// A non-nil gate holds the answer of every read until it is closed. The
	// value is read before that, as a query that is still in flight has.
	gate chan struct{}
}

func (s *reasoningRecoverySettingStore) GetValue(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	s.reads++
	value, ok := s.values[key]
	err, gate := s.readErr, s.gate
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *reasoningRecoverySettingStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *reasoningRecoverySettingStore) put(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[SettingKeyReasoningRecoveryConfig] = value
}

func (s *reasoningRecoverySettingStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func (s *reasoningRecoverySettingStore) hold() chan struct{} {
	gate := make(chan struct{})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = gate
	return gate
}

func TestReasoningRecoveryServiceDefaultsToEnabledWithoutRow(t *testing.T) {
	store := &reasoningRecoverySettingStore{values: map[string]string{}}
	recovery, err := ProvideReasoningRecoveryService(store)
	require.NoError(t, err)
	require.True(t, recovery.Enabled())
	config, err := recovery.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, ReasoningRecoveryConfig{Enabled: true}, config)
	require.Empty(t, store.values, "reading the default stores nothing")
	require.True(t, (*ReasoningRecoveryService)(nil).Enabled(), "a gateway without the service applies the default")
}

func TestReasoningRecoveryServiceSaveAppliesToTheNextRequestWithoutDatabaseRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &reasoningRecoverySettingStore{values: map[string]string{}}
	recovery, err := ProvideReasoningRecoveryService(store)
	require.NoError(t, err)
	svc := &OpenAIGatewayService{}
	svc.SetReasoningRecoveryService(recovery)
	account := &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	requestEnabled := func() bool {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		return svc.newOpenAIReasoningRecoveryState(context.Background(), c, account, "source-token").enabled
	}
	require.True(t, requestEnabled())

	saved, err := recovery.Save(context.Background(), ReasoningRecoveryConfig{Enabled: false})
	require.NoError(t, err)
	require.Equal(t, ReasoningRecoveryConfig{Enabled: false}, saved)
	require.Equal(t, map[string]string{SettingKeyReasoningRecoveryConfig: `{"enabled":false}`}, store.values)
	reads := store.readCount()
	require.False(t, requestEnabled(), "the very next request applies the saved switch")
	require.False(t, recovery.Enabled())
	require.Equal(t, reads, store.readCount(), "the forwarding path reads memory only")

	stored, err := recovery.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, ReasoningRecoveryConfig{Enabled: false}, stored)

	_, err = recovery.Save(context.Background(), ReasoningRecoveryConfig{Enabled: true})
	require.NoError(t, err)
	require.Equal(t, map[string]string{SettingKeyReasoningRecoveryConfig: `{"enabled":true}`}, store.values)
	require.True(t, requestEnabled())
}

func TestReasoningRecoveryServiceKeepsItsValueWhenTheStoredOneIsUnreadable(t *testing.T) {
	store := &reasoningRecoverySettingStore{values: map[string]string{SettingKeyReasoningRecoveryConfig: `{"enabled":false}`}}
	recovery, err := ProvideReasoningRecoveryService(store)
	require.NoError(t, err)
	require.False(t, recovery.Enabled())
	for _, raw := range []string{`true`, `{"enabled":"false"}`, `{"enabled":null}`, `{"enabled":false,"other":true}`, `{}`, ``} {
		store.put(raw)
		_, err := recovery.Get(context.Background())
		require.Error(t, err, raw)
		require.False(t, recovery.Enabled(), "a stored off never becomes the enabled default")
	}
	store.put(`{"enabled":false}`)
	store.readErr = errors.New("database unavailable")
	_, err = recovery.Get(context.Background())
	require.Error(t, err)
	require.False(t, recovery.Enabled())
	_, err = ProvideReasoningRecoveryService(store)
	require.Error(t, err, "startup does not guess the switch")
}

func TestReasoningRecoveryServiceConvergesOnAnotherInstancesSave(t *testing.T) {
	store := &reasoningRecoverySettingStore{values: map[string]string{}}
	recovery, err := ProvideReasoningRecoveryService(store)
	require.NoError(t, err)
	store.put(`{"enabled":false}`)
	require.True(t, recovery.Enabled(), "a fresh value is served from memory")

	gate := store.hold()
	recovery.nextRefresh.Store(0)
	require.True(t, recovery.Enabled(), "a stale read returns at once and refreshes in the background")
	close(gate)
	require.Eventually(t, func() bool { return !recovery.Enabled() }, time.Second, time.Millisecond)
}

// A refresh that read the stored value before a save must not publish it after
// the save: the saving instance keeps what it saved.
func TestReasoningRecoveryServiceSaveIsNotReplacedByAnEarlierRead(t *testing.T) {
	store := &reasoningRecoverySettingStore{values: map[string]string{}}
	recovery, err := ProvideReasoningRecoveryService(store)
	require.NoError(t, err)
	reads := store.readCount()
	gate := store.hold()
	recovery.nextRefresh.Store(0)
	require.True(t, recovery.Enabled())
	require.Eventually(t, func() bool { return store.readCount() > reads }, time.Second, time.Millisecond, "the refresh has read the stored default")

	saved := make(chan error, 1)
	go func() {
		_, err := recovery.Save(context.Background(), ReasoningRecoveryConfig{Enabled: false})
		saved <- err
	}()
	select {
	case err := <-saved:
		t.Fatalf("the save did not wait for the read that began before it: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(gate)
	require.NoError(t, <-saved)
	require.False(t, recovery.Enabled())
	require.Eventually(t, func() bool { return !recovery.refreshing.Load() }, time.Second, time.Millisecond)
	require.False(t, recovery.Enabled(), "the earlier read cannot replace the saved value")
}
