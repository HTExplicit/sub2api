//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type managedCindyChannelGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func TestManagedCindyCatalogChannelAdmissionUsesExactCatalogAndAliases(t *testing.T) {
	t.Parallel()

	const groupID = int64(7301)
	repo := &mockChannelRepository{
		listAllFn: func(context.Context) ([]Channel, error) {
			return []Channel{{
				ID: 91, Name: CindyCatalogChannelName, Status: StatusActive,
				BillingModelSource: BillingModelSourceChannelMapped, RestrictModels: true,
				FeaturesConfig: map[string]any{CindyCatalogChannelMarkerKey: CindyCatalogChannelMarkerValue},
				GroupIDs:       []int64{groupID},
			}}, nil
		},
		getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
			return map[int64]string{groupID: PlatformCindy}, nil
		},
	}
	svc := NewChannelService(repo, nil, nil, nil)

	for _, model := range []string{
		"gpt-5.6-luna", "openai/gpt-5.6-luna", "gpt-5.4-mini",
	} {
		require.False(t, svc.IsModelRestricted(context.Background(), groupID, model), model)
	}
	for _, model := range []string{"gpt-5.6-sol", "openai/gpt-5.6-sol", "gpt-5.4"} {
		require.True(t, svc.IsModelRestricted(context.Background(), groupID, model), model)
	}
	require.True(t, svc.IsModelRestricted(context.Background(), groupID, "not-in-cindy-catalog"))
	require.Nil(t, svc.GetChannelModelPricing(context.Background(), groupID, "gpt-5.6-sol"))

	mapped := svc.ResolveChannelMapping(context.Background(), groupID, "gpt-5.4-mini")
	require.True(t, mapped.Mapped)
	require.Equal(t, "openai/gpt-5.6-luna", mapped.MappedModel)
}

func (r managedCindyChannelGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

func TestChannelServiceRejectsManagedCindyChannelCreation(t *testing.T) {
	repo := &mockChannelRepository{}
	svc := NewChannelService(repo, nil, nil, nil)

	for _, input := range []*CreateChannelInput{
		{Name: CindyCatalogChannelName},
		{Name: "imposter", FeaturesConfig: map[string]any{CindyCatalogChannelMarkerKey: CindyCatalogChannelMarkerValue}},
	} {
		channel, err := svc.Create(context.Background(), input)
		require.ErrorIs(t, err, ErrManagedCindyChannelImmutable)
		require.Nil(t, channel)
	}
}

func TestChannelServiceRejectsCindyGroupOnOrdinaryChannel(t *testing.T) {
	groupID := int64(85)
	repo := &mockChannelRepository{}
	groups := managedCindyChannelGroupRepo{groups: map[int64]*Group{
		groupID: {
			ID: groupID, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI,
			ProviderProfile: ProviderProfileCindyLaxaV1,
		},
	}}
	svc := NewChannelService(repo, groups, nil, nil)

	channel, err := svc.Create(context.Background(), &CreateChannelInput{Name: "ordinary", GroupIDs: []int64{groupID}})

	require.ErrorIs(t, err, ErrManagedCindyChannelImmutable)
	require.Nil(t, channel)
}

func TestChannelServiceRejectsManagedCindyChannelUpdate(t *testing.T) {
	managed := &Channel{
		ID: 9, Name: CindyCatalogChannelName, Status: StatusActive,
		FeaturesConfig: map[string]any{CindyCatalogChannelMarkerKey: CindyCatalogChannelMarkerValue},
	}
	repo := &mockChannelRepository{
		getByIDFn:     func(context.Context, int64) (*Channel, error) { return managed.Clone(), nil },
		getGroupIDsFn: func(context.Context, int64) ([]int64, error) { return nil, nil },
	}
	svc := NewChannelService(repo, nil, nil, nil)

	updated, err := svc.Update(context.Background(), managed.ID, &UpdateChannelInput{Name: "renamed"})
	require.ErrorIs(t, err, ErrManagedCindyChannelImmutable)
	require.Nil(t, updated)
}
