//go:build unit

package admin

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSnapshotCache_SetAndGet(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)

	entry := c.Set("key1", map[string]string{"hello": "world"})
	require.NotEmpty(t, entry.ETag)
	require.NotNil(t, entry.Payload)

	got, ok := c.Get("key1")
	require.True(t, ok)
	require.Equal(t, entry.ETag, got.ETag)
}

func TestSnapshotCache_Expiration(t *testing.T) {
	c := newSnapshotCache(1 * time.Millisecond)

	c.Set("key1", "value")
	time.Sleep(5 * time.Millisecond)

	_, ok := c.Get("key1")
	require.False(t, ok, "expired entry should not be returned")
}

func TestSnapshotCache_GetEmptyKey(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)
	_, ok := c.Get("")
	require.False(t, ok)
}

func TestSnapshotCache_GetMiss(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)
	_, ok := c.Get("nonexistent")
	require.False(t, ok)
}

func TestSnapshotCache_NilReceiver(t *testing.T) {
	var c *snapshotCache
	_, ok := c.Get("key")
	require.False(t, ok)

	entry := c.Set("key", "value")
	require.Empty(t, entry.ETag)
}

func TestSnapshotCache_SetEmptyKey(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)

	// Set with empty key should return entry but not store it
	entry := c.Set("", "value")
	require.NotEmpty(t, entry.ETag)

	_, ok := c.Get("")
	require.False(t, ok)
}

func TestSnapshotCache_DefaultTTL(t *testing.T) {
	c := newSnapshotCache(0)
	require.Equal(t, 30*time.Second, c.ttl)

	c2 := newSnapshotCache(-1 * time.Second)
	require.Equal(t, 30*time.Second, c2.ttl)
}

func TestSnapshotCache_ETagDeterministic(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)
	payload := map[string]int{"a": 1, "b": 2}

	entry1 := c.Set("k1", payload)
	entry2 := c.Set("k2", payload)
	require.Equal(t, entry1.ETag, entry2.ETag, "same payload should produce same ETag")
}

func TestSnapshotCache_ETagFormat(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)
	entry := c.Set("k", "test")
	// ETag should be quoted hex string: "abcdef..."
	require.True(t, len(entry.ETag) > 2)
	require.Equal(t, byte('"'), entry.ETag[0])
	require.Equal(t, byte('"'), entry.ETag[len(entry.ETag)-1])
}

func TestBuildETagFromAny_UnmarshalablePayload(t *testing.T) {
	// channels are not JSON-serializable
	etag := buildETagFromAny(make(chan int))
	require.Empty(t, etag)
}

func TestSnapshotCache_GetOrLoad_MissThenHit(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)
	var loads atomic.Int32

	entry, hit, err := c.GetOrLoad("key1", func() (any, error) {
		loads.Add(1)
		return map[string]string{"hello": "world"}, nil
	})
	require.NoError(t, err)
	require.False(t, hit)
	require.NotEmpty(t, entry.ETag)
	require.Equal(t, int32(1), loads.Load())

	entry2, hit, err := c.GetOrLoad("key1", func() (any, error) {
		loads.Add(1)
		return map[string]string{"unexpected": "value"}, nil
	})
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, entry.ETag, entry2.ETag)
	require.Equal(t, int32(1), loads.Load())
}

func TestSnapshotCache_GetOrLoad_ConcurrentSingleflight(t *testing.T) {
	c := newSnapshotCache(5 * time.Second)
	var loads atomic.Int32
	start := make(chan struct{})
	const callers = 8
	errCh := make(chan error, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			_, _, err := c.GetOrLoad("shared", func() (any, error) {
				loads.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "value", nil
			})
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	require.Equal(t, int32(1), loads.Load())
}

func TestSnapshotCache_ClearDoesNotRepublishInflightResult(t *testing.T) {
	cache := newSnapshotCache(time.Minute)
	started, resume := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(resume) }) })
	go func() {
		_, _, err := cache.GetOrLoad("accounts_today_stats:42", func() (any, error) {
			close(started)
			<-resume
			return "pre-commit-stats", nil
		})
		finished <- err
	}()
	<-started
	cache.Clear()
	cache.Set("accounts_today_stats:42", "post-commit-stats")
	release.Do(func() { close(resume) })
	require.NoError(t, <-finished)
	entry, found := cache.Get("accounts_today_stats:42")
	require.True(t, found)
	require.Equal(t, "post-commit-stats", entry.Payload, "a pre-invalidation read must not replace a fresh cache entry")
}

func TestSnapshotCache_ClearSeparatesNewSingleflight(t *testing.T) {
	cache := newSnapshotCache(time.Minute)
	oldStarted, oldResume, newStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oldDone, newDone := make(chan error, 1), make(chan snapshotCacheEntry, 1)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(oldResume) }) })
	go func() {
		_, _, err := cache.GetOrLoad("same", func() (any, error) { close(oldStarted); <-oldResume; return "old", nil })
		oldDone <- err
	}()
	<-oldStarted
	cache.Clear()
	go func() {
		entry, _, _ := cache.GetOrLoad("same", func() (any, error) { close(newStarted); return "new", nil })
		newDone <- entry
	}()
	select {
	case <-newStarted:
	case <-time.After(time.Second):
		t.Fatal("new-generation caller joined a stale singleflight read")
	}
	require.Equal(t, "new", (<-newDone).Payload)
	release.Do(func() { close(oldResume) })
	require.NoError(t, <-oldDone)
	entry, found := cache.Get("same")
	require.True(t, found)
	require.Equal(t, "new", entry.Payload)
}

func TestAccountTodayStatsInvalidationFencesSampledGeneration(t *testing.T) {
	previous := accountTodayStatsBatchCache
	t.Cleanup(func() { accountTodayStatsBatchCache = previous })
	accountTodayStatsBatchCache = newSnapshotCache(time.Minute)
	key := buildAccountTodayStatsBatchCacheKey([]int64{42})
	oldGeneration := accountTodayStatsBatchCache.Generation()
	InvalidateAccountTodayStatsCache(42)
	entry, stored := accountTodayStatsBatchCache.SetIfGeneration(key, "late-pre-commit-stats", oldGeneration)
	require.False(t, stored)
	require.Equal(t, "late-pre-commit-stats", entry.Payload, "the already-running response can complete without repopulating the cache")
	_, found := accountTodayStatsBatchCache.Get(key)
	require.False(t, found)
	_, stored = accountTodayStatsBatchCache.SetIfGeneration(key, "fresh-post-commit-stats", accountTodayStatsBatchCache.Generation())
	require.True(t, stored)
	entry, found = accountTodayStatsBatchCache.Get(key)
	require.True(t, found)
	require.Equal(t, "fresh-post-commit-stats", entry.Payload)
}

func TestParseBoolQueryWithDefault(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		def  bool
		want bool
	}{
		{"empty returns default true", "", true, true},
		{"empty returns default false", "", false, false},
		{"1", "1", false, true},
		{"true", "true", false, true},
		{"TRUE", "TRUE", false, true},
		{"yes", "yes", false, true},
		{"on", "on", false, true},
		{"0", "0", true, false},
		{"false", "false", true, false},
		{"FALSE", "FALSE", true, false},
		{"no", "no", true, false},
		{"off", "off", true, false},
		{"whitespace trimmed", "  true  ", false, true},
		{"unknown returns default true", "maybe", true, true},
		{"unknown returns default false", "maybe", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseBoolQueryWithDefault(tc.raw, tc.def)
			require.Equal(t, tc.want, got)
		})
	}
}
