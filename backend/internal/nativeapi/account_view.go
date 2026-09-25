package nativeapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// AccountViewSlot and AccountConsoleSource are versioned, host-owned registries.
// A view declares data and references; it cannot select a URL, query language or
// private host component.
const (
	AccountViewSlot      = "account.view.v1"
	AccountConsoleSource = "accounts.console.v1"
	AccountViewHeader    = "X-Sub2API-Account-View"
)

type AccountViewPredicate struct {
	Platforms   []string `json:"platforms,omitempty"`
	Types       []string `json:"types,omitempty"`
	Statuses    []string `json:"statuses,omitempty"`
	Plans       []string `json:"plans,omitempty"`
	PrivacyMode string   `json:"privacy_mode,omitempty"`
}

type AccountViewPreset struct {
	ID      string               `json:"id"`
	Label   map[string]string    `json:"label"`
	Query   AccountViewPredicate `json:"query"`
	Counter string               `json:"counter"`
}

type AccountViewLayout struct {
	Kind       string `json:"kind"`
	SurfaceRef string `json:"surface_ref,omitempty"`
}

type AccountViewTable struct {
	Layouts    []string `json:"layouts"`
	ColumnRefs []string `json:"column_refs"`
	ActionRefs []string `json:"action_refs"`
}

type AccountViewNavigation struct {
	Section    string `json:"section"`
	TargetView string `json:"target_view"`
	Icon       string `json:"icon"`
	Order      int    `json:"order,omitempty"`
}

type AccountViewLegacyAlias struct {
	Match    map[string]string `json:"match"`
	Preset   string            `json:"preset"`
	Priority int               `json:"priority"`
}

type AccountViewDefinitionV1 struct {
	Version               int                      `json:"version"`
	Source                string                   `json:"source"`
	BaseQuery             AccountViewPredicate     `json:"base_query"`
	DefaultPreset         string                   `json:"default_preset"`
	ExtendsCoreFilters    bool                     `json:"extends_core_filters,omitempty"`
	CoreFilterSurfaceRefs []string                 `json:"core_filter_surface_refs,omitempty"`
	Presets               []AccountViewPreset      `json:"presets"`
	Layout                []AccountViewLayout      `json:"layout"`
	Table                 AccountViewTable         `json:"table"`
	Navigation            *AccountViewNavigation   `json:"navigation,omitempty"`
	LegacyQueryAliases    []AccountViewLegacyAlias `json:"legacy_query_aliases,omitempty"`
}

type AccountResourceActionV1 struct {
	Version          int                   `json:"version"`
	Resource         string                `json:"resource"`
	AccountParameter string                `json:"account_parameter"`
	AccountSource    string                `json:"account_source"`
	RowPredicate     *AccountViewPredicate `json:"row_predicate,omitempty"`
	Effect           string                `json:"effect"`
}

// AccountViewIdentityV1 contains no search, row data or credentials. The host
// transports its JSON as unpadded base64url in AccountViewHeader. The plugin
// operation owner remains in the existing X-Sub2API-Plugin headers; it may be a
// different installation from this origin-view owner.
type AccountViewIdentityV1 struct {
	Version              int    `json:"version"`
	PluginID             int64  `json:"plugin_id"`
	PluginKey            string `json:"plugin_key"`
	PackageSHA256        string `json:"package_sha256"`
	ViewID               string `json:"view_id"`
	PresetID             string `json:"preset_id"`
	ViewDefinitionDigest string `json:"view_definition_digest"`
}

// AccountViewQueryV1 is the normalized user-query layer. Native HTTP
// pagination stays outside this captured selection query. Folder/proxy
// identifiers are decimal strings, with uncategorized/direct sentinels.
type AccountViewQueryV1 struct {
	Platforms   []string `json:"platforms,omitempty"`
	Types       []string `json:"types,omitempty"`
	Statuses    []string `json:"statuses,omitempty"`
	Plans       []string `json:"plans,omitempty"`
	Proxies     []string `json:"proxies,omitempty"`
	Folders     []string `json:"folders,omitempty"`
	Tags        []int64  `json:"tags,omitempty"`
	AccountIDs  []int64  `json:"account_ids,omitempty"`
	GroupID     int64    `json:"group_id,omitempty"`
	PrivacyMode string   `json:"privacy_mode,omitempty"`
	Search      string   `json:"search,omitempty"`
	SortBy      string   `json:"sort_by,omitempty"`
	SortOrder   string   `json:"sort_order,omitempty"`
}

// A host-captured view_context accompanies mutation/job payloads. Full Query is
// retained only in the existing encrypted job payload, not public job metadata.
type AccountViewContextV1 struct {
	AccountViewIdentityV1
	Query AccountViewQueryV1 `json:"query"`
}

type AccountViewRecoveryAck struct {
	AccountID int64 `json:"account_id"`
	Recovered bool  `json:"recovered"`
}

func decodeAccountViewJSON(raw []byte, value any) error {
	var shape any
	if json.Unmarshal(raw, &shape) != nil || accountViewContainsNull(shape) {
		return errors.New("invalid account view JSON shape")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid account view JSON")
	}
	return nil
}

func accountViewContainsNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, child := range typed {
			if accountViewContainsNull(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if accountViewContainsNull(child) {
				return true
			}
		}
	}
	return false
}

func (view *AccountViewDefinitionV1) UnmarshalJSON(raw []byte) error {
	type plain AccountViewDefinitionV1
	var decoded plain
	if err := decodeAccountViewJSON(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, key := range []string{"version", "source", "base_query", "default_preset", "presets", "layout", "table"} {
		if len(fields[key]) == 0 || bytes.Equal(fields[key], []byte("null")) {
			return errors.New("missing required account view field")
		}
	}
	var presets []map[string]json.RawMessage
	if err := json.Unmarshal(fields["presets"], &presets); err != nil {
		return err
	}
	for _, preset := range presets {
		if len(preset["query"]) == 0 || bytes.Equal(preset["query"], []byte("null")) {
			return errors.New("missing account view preset query")
		}
	}
	*view = AccountViewDefinitionV1(decoded)
	return nil
}

func (action *AccountResourceActionV1) UnmarshalJSON(raw []byte) error {
	type plain AccountResourceActionV1
	var decoded plain
	if err := decodeAccountViewJSON(raw, &decoded); err != nil {
		return err
	}
	*action = AccountResourceActionV1(decoded)
	return nil
}

func (view *AccountViewContextV1) UnmarshalJSON(raw []byte) error {
	type plain AccountViewContextV1
	var decoded plain
	if err := decodeAccountViewJSON(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields["query"]) == 0 {
		return errors.New("missing captured account view query")
	}
	*view = AccountViewContextV1(decoded)
	return nil
}
