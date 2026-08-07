package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeBusinessSystemPromptStore struct {
	seedCalled       bool
	seed             BusinessSystemPromptSeed
	loaded           BusinessSystemPromptSnapshot
	loadErr          error
	published        BusinessSystemPromptSnapshot
	publishErr       error
	createdVersion   BusinessSystemPromptVersion
	createVersionErr error
	detail           BusinessSystemPromptTemplateDetail
	bus              *fakeBusinessSystemPromptBus
}

func (f *fakeBusinessSystemPromptStore) EnsureBusinessSystemPromptSeed(_ context.Context, seed BusinessSystemPromptSeed) error {
	f.seedCalled = true
	f.seed = seed
	return nil
}

func (f *fakeBusinessSystemPromptStore) LoadBusinessSystemPrompt(_ context.Context) (BusinessSystemPromptSnapshot, error) {
	if f.loadErr != nil {
		return BusinessSystemPromptSnapshot{}, f.loadErr
	}
	return f.loaded, nil
}

func (f *fakeBusinessSystemPromptStore) ListBusinessSystemPromptTemplates(context.Context) ([]BusinessSystemPromptTemplate, error) {
	return nil, nil
}

func (f *fakeBusinessSystemPromptStore) GetBusinessSystemPromptTemplate(context.Context, int64) (BusinessSystemPromptTemplateDetail, error) {
	return f.detail, nil
}

func (f *fakeBusinessSystemPromptStore) CreateBusinessSystemPromptTemplate(context.Context, BusinessSystemPromptTemplateCreate, int64, int64) (BusinessSystemPromptTemplateDetail, error) {
	return BusinessSystemPromptTemplateDetail{}, nil
}

func (f *fakeBusinessSystemPromptStore) UpdateBusinessSystemPromptTemplate(context.Context, int64, BusinessSystemPromptTemplateUpdate, int64, int64) (BusinessSystemPromptTemplate, error) {
	return BusinessSystemPromptTemplate{}, nil
}

func (f *fakeBusinessSystemPromptStore) CreateBusinessSystemPromptVersion(_ context.Context, _ int64, _ string, _ string, _ int64, _ int64, _ int64) (BusinessSystemPromptVersion, error) {
	return f.createdVersion, f.createVersionErr
}

func (f *fakeBusinessSystemPromptStore) DuplicateBusinessSystemPromptTemplate(context.Context, int64, string, string, int64, int64) (BusinessSystemPromptTemplateDetail, error) {
	return BusinessSystemPromptTemplateDetail{}, nil
}

func (f *fakeBusinessSystemPromptStore) SoftDeleteBusinessSystemPromptTemplate(context.Context, int64, int64, int64) error {
	return nil
}

func (f *fakeBusinessSystemPromptStore) PublishBusinessSystemPromptVersion(_ context.Context, _ int64, _ int64, _ int64, _ int64) (BusinessSystemPromptSnapshot, error) {
	return f.published, f.publishErr
}

func (f *fakeBusinessSystemPromptStore) UpdateBusinessSystemPromptRuntime(context.Context, BusinessSystemPromptRuntimeUpdate) (BusinessSystemPromptSnapshot, error) {
	return f.published, f.publishErr
}

type fakeBusinessSystemPromptBus struct {
	revisions  []int64
	publishErr error
}

func (b *fakeBusinessSystemPromptBus) Publish(_ context.Context, revision int64) error {
	b.revisions = append(b.revisions, revision)
	return b.publishErr
}

func (b *fakeBusinessSystemPromptBus) Subscribe(context.Context, func(int64)) error { return nil }

func TestBusinessSystemPromptServiceInitializeSeedsAndLoadsSnapshot(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{
		loaded: BusinessSystemPromptSnapshot{Revision: 1, Body: embeddedBusinessSystemPrompt},
	}
	svc := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, svc.Initialize(context.Background()))
	require.True(t, store.seedCalled)
	require.Equal(t, "codexrip_reverse_skill", store.seed.Slug)
	require.Equal(t, embeddedBusinessSystemPrompt, store.seed.Body)
	require.Equal(t, BusinessSystemPromptCompositionCodexSkillHybrid, store.seed.CompositionMode)
	require.Equal(t, BusinessSystemPromptRemoteSkillBundleID, store.seed.BundleID)
	require.Empty(t, store.seed.BundleManifestSHA256)
	got, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, int64(1), got.Revision)
	require.Equal(t, embeddedBusinessSystemPrompt, got.Body)
}

func TestBusinessSystemPromptServiceRejectsPublishingLegacyCompositions(t *testing.T) {
	for name, version := range map[string]BusinessSystemPromptVersion{
		"offline bundle": {
			ID: 4, CompositionMode: BusinessSystemPromptCompositionOfflineBundle,
			BundleID: "moxinggang-reverse-skill", BundleManifestSHA256: strings.Repeat("a", 64),
		},
		"remote skill": {
			ID: 4, CompositionMode: BusinessSystemPromptCompositionRemoteSkill,
			BundleID: BusinessSystemPromptRemoteSkillBundleID,
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeBusinessSystemPromptStore{
				loaded: BusinessSystemPromptSnapshot{Revision: 1, Body: "seed"},
				detail: BusinessSystemPromptTemplateDetail{Versions: []BusinessSystemPromptVersion{version}},
			}
			svc := NewBusinessSystemPromptService(store, nil)
			require.NoError(t, svc.Initialize(context.Background()))
			_, err := svc.PublishVersion(context.Background(), 3, 4, 1, 9)
			require.ErrorIs(t, err, ErrBusinessSystemPromptLegacyComposition)
		})
	}
}

func TestBusinessSystemPromptServiceRejectsCreatingOrDuplicatingLegacyOfflineBundle(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{detail: BusinessSystemPromptTemplateDetail{Versions: []BusinessSystemPromptVersion{{
		ID: 4, Body: "legacy", CompositionMode: BusinessSystemPromptCompositionOfflineBundle,
		BundleID: BusinessSystemPromptSeedBundleID, BundleManifestSHA256: BusinessSystemPromptSeedBundleManifestSHA256,
	}}}}
	svc := NewBusinessSystemPromptService(store, nil)

	_, err := svc.CreateTemplate(context.Background(), BusinessSystemPromptTemplateCreate{
		Slug: "legacy-copy", Name: "Legacy copy", Body: "legacy",
		CompositionMode: BusinessSystemPromptCompositionOfflineBundle,
		BundleID:        BusinessSystemPromptSeedBundleID, BundleManifestSHA256: BusinessSystemPromptSeedBundleManifestSHA256,
	}, 9, 1)
	require.ErrorIs(t, err, ErrBusinessSystemPromptLegacyComposition)

	_, err = svc.DuplicateTemplate(context.Background(), 3, "legacy-copy", "Legacy copy", 9, 1)
	require.ErrorIs(t, err, ErrBusinessSystemPromptLegacyComposition)
}

func TestBusinessSystemPromptServicePublishInstallsSnapshotAndBroadcastsRevision(t *testing.T) {
	bus := &fakeBusinessSystemPromptBus{}
	store := &fakeBusinessSystemPromptStore{
		loaded:    BusinessSystemPromptSnapshot{Revision: 1, Body: "old"},
		published: BusinessSystemPromptSnapshot{Revision: 2, Enabled: true, Body: "new"},
		bus:       bus,
	}
	svc := NewBusinessSystemPromptService(store, bus)
	require.NoError(t, svc.Initialize(context.Background()))
	got, err := svc.PublishVersion(context.Background(), 3, 4, 1, 9)
	require.NoError(t, err)
	require.Equal(t, "new", got.Body)
	require.Equal(t, []int64{2}, bus.revisions)
	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, int64(2), current.Revision)
}

func TestBusinessSystemPromptServiceReloadKeepsLastGoodAndMarksDegraded(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Body: "last-good"}}
	svc := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, svc.Initialize(context.Background()))
	store.loadErr = errors.New("redis/database unavailable")
	require.Error(t, svc.Reload(context.Background()))
	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, "last-good", current.Body)
	require.True(t, current.Degraded)
}

func TestBusinessSystemPromptServiceStartsWithInvalidEnabledSnapshotForRequestLevel503(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Revision: 4, Enabled: true, TemplateID: 1, VersionID: 2,
	}}
	svc := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, svc.Start(context.Background()))
	defer svc.Stop()

	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.True(t, current.Enabled)
	require.True(t, current.Degraded)
	_, _, err := ApplyBusinessSystemPromptToJSON(
		[]byte(`{"input":[]}`), current,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses},
	)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestBusinessSystemPromptServiceCanDisableCorruptActiveSnapshot(t *testing.T) {
	bus := &fakeBusinessSystemPromptBus{}
	store := &fakeBusinessSystemPromptStore{
		loaded: BusinessSystemPromptSnapshot{Revision: 1, Enabled: true, Body: "last-good"},
		published: BusinessSystemPromptSnapshot{
			Revision: 2, Enabled: false, TemplateID: 1, VersionID: 2,
			Body: "corrupt\x00body", SHA256: "stale", ByteLength: 12,
		},
	}
	svc := NewBusinessSystemPromptService(store, bus)
	require.NoError(t, svc.Initialize(context.Background()))

	got, err := svc.UpdateRuntime(context.Background(), BusinessSystemPromptRuntimeUpdate{ExpectedRevision: 1})
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.True(t, got.Degraded)
	require.Equal(t, []int64{2}, bus.revisions)

	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.False(t, current.Enabled)
	require.True(t, current.Degraded)
	body := []byte(`{"instructions":"client"}`)
	updated, application, err := ApplyBusinessSystemPromptToJSON(
		body,
		current,
		BusinessSystemPromptTarget{Platform: PlatformOpenAI, Protocol: BusinessSystemPromptProtocolResponses},
	)
	require.NoError(t, err)
	require.False(t, application.Applied)
	require.Equal(t, body, updated)
}

func TestBusinessSystemPromptServiceReloadNeverDowngradesRevision(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 3, Body: "new"}}
	svc := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, svc.Initialize(context.Background()))

	store.loaded = BusinessSystemPromptSnapshot{Revision: 2, Body: "stale"}
	require.NoError(t, svc.Reload(context.Background()))
	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, int64(3), current.Revision)
	require.Equal(t, "new", current.Body)
}

func TestBusinessSystemPromptServicePublishBusFailureReturnsCommittedDegradedSnapshot(t *testing.T) {
	bus := &fakeBusinessSystemPromptBus{publishErr: errors.New("redis unavailable")}
	store := &fakeBusinessSystemPromptStore{
		loaded:    BusinessSystemPromptSnapshot{Revision: 1, Body: "old"},
		published: BusinessSystemPromptSnapshot{Revision: 2, Enabled: true, Body: "new"},
	}
	svc := NewBusinessSystemPromptService(store, bus)
	require.NoError(t, svc.Initialize(context.Background()))

	got, err := svc.PublishVersion(context.Background(), 3, 4, 1, 9)
	require.NoError(t, err)
	require.True(t, got.Degraded)
	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.True(t, current.Degraded)
	require.Equal(t, int64(2), current.Revision)
}

func TestBusinessSystemPromptServiceRejectsInvalidDraftBeforeStore(t *testing.T) {
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{Revision: 1, Body: "seed"}}
	svc := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, svc.Initialize(context.Background()))
	_, err := svc.CreateVersion(context.Background(), 1, " \n", "", 7, 1, 1)
	require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
	require.Empty(t, store.createdVersion)
}
