package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedModelCatalogIsolationFilterPreservesSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
		want []string
	}{
		{name: "nil"},
		{name: "empty", ids: []string{}, want: []string{}},
		{name: "reserved only", ids: []string{"s2pub-route", " \tS2PuB-SECOND\n"}, want: []string{}},
		{name: "unchanged legal spelling", ids: []string{" Original-Z ", "provider/s2pub-model", "s2public", "Original-A"}, want: []string{" Original-Z ", "provider/s2pub-model", "s2public", "Original-A"}},
		{name: "mixed keeps order and duplicates", ids: []string{"Original-Z", "s2pub-route", " Original-A ", "S2PUB-SECOND", "Original-Z"}, want: []string{"Original-Z", " Original-A ", "Original-Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.ids)
			require.Equal(t, tc.want, FilterManagedModelSelectors(tc.ids))
			require.Equal(t, before, tc.ids, "filtering must not overwrite a cached or shared source slice")
		})
	}
}

func TestManagedModelCatalogIsolationPrivateGroupsPreserveMappings(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowlist GroupModelAllowlist
	}{
		{name: "no allowlist"},
		{name: "unrestricted allowlist", allowlist: GroupModelAllowlist{Enabled: true, Models: []string{"*"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &Group{ID: 7, Platform: PlatformOpenAI, IsExclusive: true, ModelAllowlist: tc.allowlist}
			account := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{
				"model_mapping": map[string]any{
					"Original-Z":  "vendor/original-z",
					"s2pub-route": "vendor/managed-z",
					"Original-A":  "vendor/original-a",
					"S2PUB-OTHER": "vendor/managed-a",
				},
			}}
			before, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			mapping := account.GetModelMapping()
			ids := []string{"Original-Z", "s2pub-route", "Original-A", "S2PUB-OTHER"}
			for _, id := range ids {
				require.Contains(t, mapping, id)
			}
			originalIDs := slices.Clone(ids)
			want := []string{"Original-Z", "Original-A"}
			require.False(t, group.ManagedModelRoutes.Enabled)
			require.Equal(t, want, group.ModelAllowlist.FilterForListing(FilterManagedModelSelectors(ids)))
			require.Equal(t, want, FilterCodexModelIDsForGroup(ids, group))
			require.Equal(t, originalIDs, ids)
			require.Equal(t, map[string]string{
				"Original-Z": "vendor/original-z", "s2pub-route": "vendor/managed-z",
				"Original-A": "vendor/original-a", "S2PUB-OTHER": "vendor/managed-a",
			}, account.GetModelMapping(), "internal routing and private aliases must retain their original targets")
			after, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestManagedModelCatalogIsolationMergePreservesDescriptors(t *testing.T) {
	const first = `{"slug":"Original-Z","context_window":123456,"max_context_window":234567,"auto_compact_token_limit":120000,"custom":{"flags":[true,"keep"]},"visibility":"list"}`
	const second = `{"slug":"Original-A","context_window":345678,"max_context_window":456789,"auto_compact_token_limit":null,"custom":"unchanged","supported_reasoning_levels":[{"effort":"high"}]}`
	const input = `{"models":[` + first + `,{"slug":" \tS2PuB-UPSTREAM "},` + second + `],"metadata":{"revision":9,"unknown":[1,true]}}`
	for _, tc := range []struct {
		name      string
		selected  []string
		selection bool
	}{
		{name: "no allowlist"},
		{name: "unrestricted allowlist", selected: []string{"*"}, selection: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(input)
			configured := []string{"s2pub-configured", " \tS2PUB-CONFIGURED-TWO ", "New-Public"}
			beforeConfigured, beforeSelected := slices.Clone(configured), slices.Clone(tc.selected)
			got, changed, err := mergeConfiguredCodexModelsManifest(body, configured, tc.selected, tc.selection)
			require.NoError(t, err)
			require.True(t, changed)
			var envelope struct {
				Models   []json.RawMessage `json:"models"`
				Metadata json.RawMessage   `json:"metadata"`
			}
			require.NoError(t, json.Unmarshal(got, &envelope))
			require.Len(t, envelope.Models, 3)
			require.JSONEq(t, first, string(envelope.Models[0]))
			require.JSONEq(t, second, string(envelope.Models[1]))
			var added struct {
				Slug string `json:"slug"`
			}
			require.NoError(t, json.Unmarshal(envelope.Models[2], &added))
			require.Equal(t, "New-Public", added.Slug)
			require.JSONEq(t, `{"revision":9,"unknown":[1,true]}`, string(envelope.Metadata))
			require.Equal(t, input, string(body), "the shared upstream source body must remain unchanged")
			require.Equal(t, beforeConfigured, configured)
			require.Equal(t, beforeSelected, tc.selected)
		})
	}
}

func TestManagedModelCatalogIsolationCapacityFiltersBeforeResolving(t *testing.T) {
	for _, tc := range []struct {
		name    string
		listKey string
		idKey   string
		codex   bool
	}{
		{name: "ordinary models", listKey: "data", idKey: "id"},
		{name: "Codex manifest", listKey: "models", idKey: "slug", codex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legalRow := func(model string) string {
				return fmt.Sprintf(`{%q:%q,"context_window":8000,"max_context_window":9000,"auto_compact_token_limit":1000,"custom":{"flags":[true,3]}}`, tc.idKey, model)
			}
			protectedRow := fmt.Sprintf(`{%q:"cindy-opus","context_window":77777,"max_context_window":99999,"auto_compact_token_limit":null,"custom":{"pinned":true}}`, tc.idKey)
			input := fmt.Sprintf(`{%q:[%s,{%q:" \tS2PuB-HIDDEN "},%s,{%q:"s2pub-protected"},%s],"metadata":{"unknown":true}}`,
				tc.listKey, legalRow("Original-Z"), tc.idKey, protectedRow, tc.idKey, legalRow("Original-A"))
			body := []byte(input)
			var resolved []string
			got, err := projectModelCapacityEnvelope(body, tc.codex, func(model string) ResolvedModelContextCapacity {
				resolved = append(resolved, model)
				return ResolvedModelContextCapacity{ModelContextCapacity: ModelContextCapacity{
					ContextWindow: 32000, MaxContextWindow: 64000, MaxInputTokens: 30000, MaxOutputTokens: 2000, CapacityBasis: "total_context",
				}, Source: "custom"}
			}, map[string]bool{"cindy-opus": true, "s2pub-protected": true})
			require.NoError(t, err)
			require.Equal(t, []string{"Original-Z", "Original-A"}, resolved, "reserved and protected rows must not invoke capacity resolution")
			var envelope map[string]json.RawMessage
			var rows []json.RawMessage
			require.NoError(t, json.Unmarshal(got, &envelope))
			require.NoError(t, json.Unmarshal(envelope[tc.listKey], &rows))
			require.Len(t, rows, 3)
			for i, model := range map[int]string{0: "Original-Z", 2: "Original-A"} {
				want := fmt.Sprintf(`{%q:%q,"context_window":32000,"max_context_window":64000,"max_input_tokens":30000,"max_output_tokens":2000,"context_capacity_source":"custom","context_capacity_basis":"total_context","auto_compact_token_limit":1000,"custom":{"flags":[true,3]}}`, tc.idKey, model)
				require.JSONEq(t, want, string(rows[i]))
			}
			require.JSONEq(t, protectedRow, string(rows[1]), "legal pinned Cindy metadata must not receive a capacity overlay")
			require.JSONEq(t, `{"unknown":true}`, string(envelope["metadata"]))
			require.Equal(t, input, string(body))
		})
	}
}

func TestManagedModelCatalogIsolationEmptyCatalogNeverAddsDefaults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		models     string
		configured []string
	}{
		{name: "empty", models: `[]`},
		{name: "upstream reserved only", models: `[{"slug":"s2pub-only"},{"slug":" S2PUB-SECOND "}]`},
		{name: "configured reserved only", models: `[]`, configured: []string{"s2pub-only", " S2PUB-SECOND "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"models":` + tc.models + `,"metadata":{"keep":true}}`)
			got, _, err := mergeConfiguredCodexModelsManifest(body, tc.configured, nil, false)
			require.NoError(t, err)
			require.JSONEq(t, `{"models":[],"metadata":{"keep":true}}`, string(got))
			require.Empty(t, FilterCodexModelIDsForGroup(tc.configured, &Group{Platform: PlatformOpenAI}))
			standalone, err := BuildCodexModelsManifest(tc.configured)
			require.NoError(t, err)
			require.JSONEq(t, `{"models":[]}`, string(standalone))
		})
	}
	for _, tc := range []struct {
		name  string
		input string
		want  string
		codex bool
	}{
		{name: "ordinary empty", input: `{"data":[]}`, want: `{"data":[]}`},
		{name: "ordinary reserved only", input: `{"data":[{"id":" S2PUB-only "}]}`, want: `{"data":[]}`},
		{name: "Codex empty", input: `{"models":[]}`, want: `{"models":[]}`, codex: true},
		{name: "Codex reserved only", input: `{"models":[{"slug":" S2PUB-only "}]}`, want: `{"models":[]}`, codex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectModelCapacityEnvelope([]byte(tc.input), tc.codex, func(model string) ResolvedModelContextCapacity {
				t.Errorf("an empty visible catalog must not resolve capacity for %q", model)
				return ResolvedModelContextCapacity{Source: "protected"}
			}, nil)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(got))
		})
	}
}
