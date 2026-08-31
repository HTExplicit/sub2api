package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type openAIRefusalRuntimeRepo struct {
	mu               sync.Mutex
	values           map[string]string
	getValueCalls    int
	getMultipleCalls int
	getValueErr      error
	getMultipleErr   error
	getMultipleStart chan<- struct{}
	getMultipleWait  <-chan struct{}
}

func TestGatewayForwardingSettingsWithoutRepositoryUseSafeDefaults(t *testing.T) {
	service := &SettingService{}
	require.Equal(t, OpenAITTFTModeSemantic, service.GetOpenAITTFTMode(context.Background()))
	fingerprint, metadata, cch := service.GetGatewayForwardingSettings(context.Background())
	require.True(t, fingerprint)
	require.False(t, metadata)
	require.False(t, cch)
	enabled, prompt, blocks := service.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())
	require.True(t, enabled)
	require.Empty(t, prompt)
	require.Empty(t, blocks)
	require.True(t, service.IsClientDatelineNormalizationEnabled(context.Background()))
}

func (r *openAIRefusalRuntimeRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *openAIRefusalRuntimeRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getValueCalls++
	if r.getValueErr != nil {
		return "", r.getValueErr
	}
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *openAIRefusalRuntimeRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *openAIRefusalRuntimeRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	r.getMultipleCalls++
	if r.getMultipleErr != nil {
		err := r.getMultipleErr
		r.mu.Unlock()
		return nil, err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	start := r.getMultipleStart
	wait := r.getMultipleWait
	r.getMultipleStart = nil
	r.getMultipleWait = nil
	r.mu.Unlock()
	if start != nil {
		start <- struct{}{}
	}
	if wait != nil {
		<-wait
	}
	return out, nil
}

func (r *openAIRefusalRuntimeRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *openAIRefusalRuntimeRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *openAIRefusalRuntimeRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func (r *openAIRefusalRuntimeRepo) failMultiple(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getMultipleErr = err
}

func (r *openAIRefusalRuntimeRepo) blockNextMultiple(start chan<- struct{}, wait <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getMultipleStart = start
	r.getMultipleWait = wait
}

func (r *openAIRefusalRuntimeRepo) calls() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getValueCalls, r.getMultipleCalls
}

func openAIRefusalRuntimeValues(rewrite bool) map[string]string {
	values := map[string]string{
		SettingKeyOpenAIRefusalRecoveryEnabled:                   "true",
		SettingKeyOpenAICyberFailoverEnabled:                     "true",
		SettingKeyOpenAIAPIKeyAlphaSearchResponsesBridgeEnabled:  "true",
		SettingKeyOpenAIAPIKeyPromptCacheKeyNormalizationEnabled: "true",
		SettingKeyOpenAIRefusalRewriteEnabled:                    "false",
		SettingKeyOpenAIRefusalKeywords:                          `["cannot"]`,
		SettingKeyOpenAIRefusalReplacement:                       "continue current task",
	}
	if rewrite {
		values[SettingKeyOpenAIRefusalRewriteEnabled] = "true"
	}
	return values
}

func TestOpenAIRefusalRuntimeLoadsAPIKeyCompatibilitySwitches(t *testing.T) {
	repo := &openAIRefusalRuntimeRepo{values: openAIRefusalRuntimeValues(false)}
	svc := &SettingService{settingRepo: repo}

	runtime := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())

	require.True(t, runtime.APIKeyAlphaSearchResponsesBridge)
	require.True(t, runtime.APIKeyPromptCacheKeyNormalization)
}

func expireOpenAIRefusalRuntimeCache(t *testing.T, svc *SettingService) {
	t.Helper()
	entry, ok := svc.openAIRefusalRecoveryCache.Load().(*cachedOpenAIRefusalRecoveryRuntime)
	require.True(t, ok)
	require.NotNil(t, entry)
	svc.openAIRefusalRecoveryCache.Store(&cachedOpenAIRefusalRecoveryRuntime{
		runtime:   entry.runtime,
		expiresAt: time.Now().Add(-time.Second).UnixNano(),
	})
}

func TestOpenAIRefusalRuntimeKeepsLastKnownSnapshotOnRefreshError(t *testing.T) {
	repo := &openAIRefusalRuntimeRepo{values: openAIRefusalRuntimeValues(true)}
	svc := &SettingService{settingRepo: repo}

	initial := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())
	require.True(t, initial.RewriteEnabled())
	expireOpenAIRefusalRuntimeCache(t, svc)
	repo.failMultiple(errors.New("temporary settings read failure"))

	stale := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())
	require.True(t, stale.RewriteEnabled())
	matched, _ := stale.Matcher.MatchLeadingParagraphs("I cannot help.")
	require.True(t, matched)

	again := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())
	require.True(t, again.RewriteEnabled())
	getValueCalls, getMultipleCalls := repo.calls()
	require.Equal(t, 0, getValueCalls)
	require.Equal(t, 2, getMultipleCalls)
}

func TestOpenAIRefusalRuntimeLoadsCyberWhenRefusalSwitchOff(t *testing.T) {
	values := openAIRefusalRuntimeValues(true)
	values[SettingKeyOpenAIRefusalRecoveryEnabled] = "false"
	repo := &openAIRefusalRuntimeRepo{values: values}
	svc := &SettingService{settingRepo: repo}

	runtime := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())

	require.False(t, runtime.Enabled)
	require.True(t, runtime.CyberFailoverEnabled())
	require.False(t, runtime.RewriteEnabled())
	require.Nil(t, runtime.Matcher)
	getValueCalls, getMultipleCalls := repo.calls()
	require.Equal(t, 0, getValueCalls)
	require.Equal(t, 1, getMultipleCalls)
}

func TestOpenAIRefusalRuntimeInitialRefreshErrorDefaultsToDisabled(t *testing.T) {
	repo := &openAIRefusalRuntimeRepo{values: openAIRefusalRuntimeValues(true)}
	repo.failMultiple(errors.New("temporary initial settings read failure"))
	svc := &SettingService{settingRepo: repo}

	initial := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())

	require.False(t, initial.Enabled)
	require.False(t, initial.CyberFailoverEnabled())
	require.False(t, initial.RewriteEnabled())
}

func TestOpenAIRefusalRuntimeRewriteDisabledKeepsCyberFailoverWithoutMatcher(t *testing.T) {
	repo := &openAIRefusalRuntimeRepo{values: openAIRefusalRuntimeValues(false)}
	svc := &SettingService{settingRepo: repo}

	runtime := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())

	require.True(t, runtime.Enabled)
	require.True(t, runtime.CyberFailoverEnabled())
	require.False(t, runtime.RewriteEnabled())
	require.False(t, runtime.Rewrite)
	require.Nil(t, runtime.Matcher)
}

func TestOpenAIRefusalRuntimeRefreshDoesNotOverwriteNewSettings(t *testing.T) {
	repo := &openAIRefusalRuntimeRepo{values: openAIRefusalRuntimeValues(false)}
	svc := &SettingService{settingRepo: repo}
	initial := svc.GetOpenAIRefusalRecoveryRuntime(context.Background())
	require.False(t, initial.RewriteEnabled())
	expireOpenAIRefusalRuntimeCache(t, svc)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	repo.blockNextMultiple(started, release)
	result := make(chan OpenAIRefusalRecoveryRuntime, 1)
	go func() {
		result <- svc.GetOpenAIRefusalRecoveryRuntime(context.Background())
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("settings refresh did not reach GetMultiple")
	}

	svc.refreshCachedSettings(&SystemSettings{
		OpenAIRefusalRecoveryEnabled: true,
		OpenAICyberFailoverEnabled:   true,
		OpenAIRefusalRewriteEnabled:  true,
		OpenAIRefusalKeywords:        []string{"cannot"},
		OpenAIRefusalReplacement:     "continue current task",
	})
	close(release)

	select {
	case runtime := <-result:
		require.True(t, runtime.RewriteEnabled())
	case <-time.After(5 * time.Second):
		t.Fatal("settings refresh did not complete")
	}
	require.True(t, svc.GetOpenAIRefusalRecoveryRuntime(context.Background()).RewriteEnabled())
}
