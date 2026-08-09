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
	seeds            []BusinessSystemPromptSeed
	loaded           BusinessSystemPromptSnapshot
	loadErr          error
	published        BusinessSystemPromptSnapshot
	publishErr       error
	createdVersion   BusinessSystemPromptVersion
	createVersionErr error
	sourceSyncResult BusinessSystemPromptSourceSyncResult
	sourceSyncErr    error
	sourceSyncCalls  int
	sourceTemplateID int64
	sourceCandidate  BusinessSystemPromptSourceCandidate
	sourceActorID    int64
	sourceLatest     int64
	sourceRevision   int64
	detail           BusinessSystemPromptTemplateDetail
	bus              *fakeBusinessSystemPromptBus
}

func (f *fakeBusinessSystemPromptStore) EnsureBusinessSystemPromptSeed(_ context.Context, seed BusinessSystemPromptSeed) error {
	f.seedCalled = true
	f.seed = seed
	f.seeds = append(f.seeds, seed)
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

func (f *fakeBusinessSystemPromptStore) SyncBusinessSystemPromptSourceVersion(_ context.Context, templateID int64, candidate BusinessSystemPromptSourceCandidate, actorID, expectedLatestVersion, expectedRevision int64) (BusinessSystemPromptSourceSyncResult, error) {
	f.sourceSyncCalls++
	f.sourceTemplateID = templateID
	f.sourceCandidate = candidate
	f.sourceActorID = actorID
	f.sourceLatest = expectedLatestVersion
	f.sourceRevision = expectedRevision
	return f.sourceSyncResult, f.sourceSyncErr
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

type fakeBusinessSystemPromptSource struct {
	candidate BusinessSystemPromptSourceCandidate
	err       error
	calls     int
}

func (f *fakeBusinessSystemPromptSource) Fetch(context.Context) (BusinessSystemPromptSourceCandidate, error) {
	f.calls++
	return f.candidate, f.err
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
	require.Len(t, store.seeds, 2)
	require.Equal(t, "codexrip_reverse_skill", store.seeds[0].Slug)
	require.Equal(t, embeddedBusinessSystemPrompt, store.seeds[0].Body)
	require.Equal(t, BusinessSystemPromptCompositionCodexSkillHybrid, store.seeds[0].CompositionMode)
	require.Equal(t, BusinessSystemPromptRemoteSkillBundleID, store.seeds[0].BundleID)
	require.Empty(t, store.seeds[0].BundleManifestSHA256)
	require.True(t, store.seeds[0].UpgradeExistingSeed)
	require.ElementsMatch(t, []string{
		"0b717f086b1bf25e8300e9f26578ee95cf6f74d5601c06b9f9e493aa8939b0a7",
		"9143d8a97727030192a62fb19f732b0823dec9ffe83081ef5ae27fdb1edfea04",
		"0615d24958a1da11edcf9538aaff989e46fcd296ea86a6c1b1af2b3efa48487f",
	}, store.seeds[0].AutoActivateFromSHA)
	require.Equal(t, "gpt_5_6_instruct", store.seeds[1].Slug)
	require.Equal(t, BusinessSystemPromptCompositionInline, store.seeds[1].CompositionMode)
	require.Equal(t, "c71c50e2f7a303b5eebc2b24c0b1ca0d9c753e3240db05c3e472c679907898f7", store.seeds[1].SHA256)
	require.Equal(t, 5170, store.seeds[1].ByteLength)
	require.Equal(t, BusinessSystemPromptManagedSourceGPT56, store.seeds[1].ManagedSource)
	require.Equal(t, "MDX-Tom/gpt-5.6-instruct", store.seeds[1].SourceRepository)
	require.Equal(t, "77e7a649903f9556f2d7bfa0223fa99e123aad52", store.seeds[1].SourceCommit)
	require.Equal(t, "v45", store.seeds[1].SourceVersion)
	require.Equal(t, "gpt-5.6-sol-unrestricted-v45.zip", store.seeds[1].SourceArtifact)
	require.Equal(t, "c86c2c6d20a4d1155d87422f485eb37b77539132270918c002b5d8237a5adf54", store.seeds[1].SourceArtifactSHA256)
	require.Equal(t, GPT56PromptLicenseSHA256, store.seeds[1].SourceLicenseSHA256)
	got, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, int64(1), got.Revision)
	require.Equal(t, embeddedBusinessSystemPrompt, got.Body)
}

func TestBusinessSystemPromptServiceSyncManagedSourceCreatesCandidateWithoutChangingRuntime(t *testing.T) {
	candidate := BusinessSystemPromptSourceCandidate{
		ManagedSource:    BusinessSystemPromptManagedSourceGPT56,
		SourceRepository: gpt56PromptRepository, SourceCommit: strings.Repeat("d", 40),
		SourceVersion: "v46", SourceArtifact: "gpt-5.6-sol-unrestricted-v46.zip",
		SourceArtifactSHA256: strings.Repeat("e", 64), SourceLicenseSHA256: GPT56PromptLicenseSHA256,
		Body: "new prompt", SHA256: "c8e794dbda1ae17d4b49d82f31eae032220a555087a8b2693c0f83f84c32d9e2", ByteLength: 10,
	}
	created := BusinessSystemPromptVersion{ID: 35, TemplateID: 12, Version: 2, Body: candidate.Body, SHA256: candidate.SHA256}
	store := &fakeBusinessSystemPromptStore{
		loaded: BusinessSystemPromptSnapshot{Revision: 4, Enabled: true, TemplateID: 1, VersionID: 2, Body: "active"},
		sourceSyncResult: BusinessSystemPromptSourceSyncResult{
			Status: BusinessSystemPromptSourceSyncCandidateCreated, Version: &created,
		},
	}
	bus := &fakeBusinessSystemPromptBus{}
	source := &fakeBusinessSystemPromptSource{candidate: candidate}
	svc := NewBusinessSystemPromptService(store, bus)
	svc.SetBusinessSystemPromptSource(source)
	require.NoError(t, svc.Initialize(context.Background()))

	result, err := svc.SyncManagedSource(context.Background(), 12, 9, 1, 4)
	require.NoError(t, err)
	require.Equal(t, BusinessSystemPromptSourceSyncCandidateCreated, result.Status)
	require.Equal(t, 1, source.calls)
	require.Equal(t, 1, store.sourceSyncCalls)
	require.Equal(t, int64(12), store.sourceTemplateID)
	require.Equal(t, candidate, store.sourceCandidate)
	require.Equal(t, int64(9), store.sourceActorID)
	require.Equal(t, int64(1), store.sourceLatest)
	require.Equal(t, int64(4), store.sourceRevision)
	require.Empty(t, bus.revisions)
	current, ok := svc.CurrentSnapshot()
	require.True(t, ok)
	require.Equal(t, int64(4), current.Revision)
	require.Equal(t, "active", current.Body)
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
