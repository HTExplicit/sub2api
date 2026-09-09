package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloneGroupForDuplicateRemovesOnlyExactManagedV2Selectors(t *testing.T) {
	const groupID int64 = 23
	firstModel, secondModel := "claude-fable-5.1", "claude-fable-5"
	selectors := []string{
		ManagedModelBranchSelector(groupID, firstModel, PlatformAnthropic, "messages", "claude-fable-5.1"),
		ManagedModelBranchSelector(groupID, firstModel, PlatformOpenAI, "responses", "fable-5.1"),
		ManagedModelBranchSelector(groupID, firstModel, PlatformOpenAI, "chat_completions", "provider/fable-5.1-CC"),
		ManagedModelBranchSelector(groupID, secondModel, PlatformOpenAI, "responses", "fable-5"),
	}
	privateModels := []string{
		"private-model",
		"s2pub-unknown-private-model",
		selectors[0] + "-private",
		strings.ToUpper(selectors[1]),
		" " + selectors[2] + " ",
	}
	type testCase struct {
		name    string
		model   string
		removed bool
	}
	var cases []testCase
	for i, selector := range selectors {
		cases = append(cases, testCase{fmt.Sprintf("known_branch_%d", i), selector, true})
	}
	for i, model := range privateModels {
		cases = append(cases, testCase{fmt.Sprintf("private_model_%d", i), model, false})
	}
	cases = append(cases, testCase{name: "empty_private_model"})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			privateRouting := make(map[string][]int64, len(privateModels))
			privateMappings := make(map[string]string, len(privateModels))
			source := &Group{
				ID:                    groupID,
				Name:                  "public-models",
				Platform:              PlatformAnthropic,
				Status:                StatusActive,
				RateMultiplier:        0.5,
				ModelRoutingEnabled:   true,
				AllowMessagesDispatch: true,
				DefaultMappedModel:    tc.model,
				ModelRouting:          make(map[string][]int64),
				MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
					OpusMappedModel:    tc.model,
					SonnetMappedModel:  tc.model,
					HaikuMappedModel:   tc.model,
					ExactModelMappings: make(map[string]string),
				},
				ModelAllowlist: GroupModelAllowlist{Enabled: true},
				ManagedModelRoutes: ManagedModelRoutesConfig{
					Version: 2,
					Enabled: true,
					Routes: []ManagedModelRoute{
						{PublicModel: firstModel, Branches: []ManagedModelRouteBranch{
							{Selector: selectors[0], TargetPlatform: PlatformAnthropic, UpstreamProtocol: "messages"},
							{Selector: selectors[1], TargetPlatform: PlatformOpenAI, UpstreamProtocol: "responses"},
							{Selector: selectors[2], TargetPlatform: PlatformOpenAI, UpstreamProtocol: "chat_completions"},
						}},
						{PublicModel: secondModel, Branches: []ManagedModelRouteBranch{
							{Selector: selectors[3], TargetPlatform: PlatformOpenAI, UpstreamProtocol: "responses"},
						}},
					},
				},
			}
			for i, model := range privateModels {
				privateRouting[model] = []int64{41, 42}
				source.ModelRouting[model] = []int64{41, 42}
				// Keep unknown selector-like values on both sides of private mappings.
				privateMappings[model] = privateModels[(i+1)%len(privateModels)]
				source.MessagesDispatchModelConfig.ExactModelMappings[model] = privateMappings[model]
			}
			for i, selector := range selectors {
				source.ModelRouting[selector] = []int64{51, 52}
				source.MessagesDispatchModelConfig.ExactModelMappings[selector] = "private-target"
				source.MessagesDispatchModelConfig.ExactModelMappings[fmt.Sprintf("public-alias-%d", i)] = selector
				source.ModelAllowlist.Models = append(source.ModelAllowlist.Models, selector, privateModels[i])
			}
			source.ModelAllowlist.Models = append(source.ModelAllowlist.Models, privateModels[len(selectors):]...)
			before, err := json.Marshal(source)
			require.NoError(t, err)

			duplicate := cloneGroupForDuplicate(source, "duplicate-operation")

			require.Equal(t, ManagedModelRoutesConfig{}, duplicate.ManagedModelRoutes,
				"a copied group must not retain any published branch from the source group")
			require.Equal(t, privateRouting, duplicate.ModelRouting)
			require.Equal(t, privateMappings, duplicate.MessagesDispatchModelConfig.ExactModelMappings,
				"remove mappings with an exact known selector on either side only")
			require.Equal(t, GroupModelAllowlist{Enabled: true, Models: privateModels}, duplicate.ModelAllowlist)
			expectedModel := tc.model
			if tc.removed {
				expectedModel = ""
			}
			require.Equal(t, expectedModel, duplicate.DefaultMappedModel)
			require.Equal(t, expectedModel, duplicate.MessagesDispatchModelConfig.OpusMappedModel)
			require.Equal(t, expectedModel, duplicate.MessagesDispatchModelConfig.SonnetMappedModel)
			require.Equal(t, expectedModel, duplicate.MessagesDispatchModelConfig.HaikuMappedModel)
			require.Equal(t, source.RateMultiplier, duplicate.RateMultiplier)
			require.Equal(t, source.ModelRoutingEnabled, duplicate.ModelRoutingEnabled)
			require.Equal(t, source.AllowMessagesDispatch, duplicate.AllowMessagesDispatch)
			require.Equal(t, "duplicate-operation", duplicate.DuplicateOperationID)

			after, err := json.Marshal(source)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "cleanup must not mutate the source group")

			duplicate.ModelRouting[privateModels[0]][0] = 999
			duplicate.MessagesDispatchModelConfig.ExactModelMappings[privateModels[0]] = "changed"
			duplicate.ModelAllowlist.Models[0] = "changed"
			after, err = json.Marshal(source)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "retained private maps and slices must be independent copies")
		})
	}
}
