package extensionv1

import (
	"encoding/json"
	"errors"
)

const AccountEditSlot = "account.edit.v1"

type AccountEditCredentialUIV1 struct {
	BaseURL         string            `json:"base_url"`
	BaseURLReadonly bool              `json:"base_url_readonly"`
	Hint            map[string]string `json:"hint"`
}

type AccountEditWireControlV1 struct {
	Target     string   `json:"target"`
	Values     []string `json:"values"`
	AllowClear bool     `json:"allow_clear"`
}

type AccountEditLabelsV1 struct {
	ManagedCatalog map[string]string `json:"managed_catalog"`
	ManagedAliases map[string]string `json:"managed_aliases"`
	CustomMappings map[string]string `json:"custom_mappings"`
}

// AccountEditDefinitionV1 declares finite policy. Native account state and
// credentials never cross the public extension boundary.
type AccountEditDefinitionV1 struct {
	Version                int                        `json:"version"`
	Platform               string                     `json:"platform"`
	AccountType            string                     `json:"account_type"`
	CredentialProfile      string                     `json:"credential_profile"`
	CredentialUI           AccountEditCredentialUIV1  `json:"credential_ui"`
	CatalogSource          string                     `json:"catalog_source"`
	MappingPolicy          string                     `json:"mapping_policy"`
	CompactMappingEditable bool                       `json:"compact_mapping_editable"`
	WireControls           []AccountEditWireControlV1 `json:"wire_controls"`
	UnlistedFields         string                     `json:"unlisted_fields"`
	Labels                 AccountEditLabelsV1        `json:"labels"`
}

// A nil Value distinguishes mapping intent and clear from a mode set. Mode
// enums and registered targets are validated by the host, not executable code.
type AccountEditChangeV1 struct {
	Op    string  `json:"op"`
	Value *string `json:"value,omitempty"`
}

type ProviderEditRequestV1 struct {
	ContributionID            string                         `json:"contribution_id"`
	ExpectedPackageSHA256     string                         `json:"expected_package_sha256"`
	ExpectedDefinitionSHA256  string                         `json:"expected_definition_sha256"`
	ExpectedRuntimeGeneration int64                          `json:"expected_runtime_generation"`
	ExpectedStateSHA256       string                         `json:"expected_state_sha256"`
	ExpectedCatalogNamespace  string                         `json:"expected_catalog_namespace,omitempty"`
	Changes                   map[string]AccountEditChangeV1 `json:"changes"`
}

func (definition *AccountEditDefinitionV1) UnmarshalJSON(raw []byte) error {
	type plain AccountEditDefinitionV1
	var decoded plain
	if err := decodeAccountCreateJSON(raw, &decoded, false, "version", "platform", "account_type", "credential_profile", "credential_ui", "catalog_source", "mapping_policy", "compact_mapping_editable", "wire_controls", "unlisted_fields", "labels"); err != nil {
		return errors.New("invalid account edit definition")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for field, required := range map[string][]string{
		"credential_ui": {"base_url", "base_url_readonly", "hint"},
		"labels":        {"managed_catalog", "managed_aliases", "custom_mappings"},
	} {
		var members map[string]json.RawMessage
		_ = json.Unmarshal(fields[field], &members)
		for _, key := range required {
			if _, present := members[key]; !present {
				return errors.New("missing account edit definition member")
			}
		}
	}
	var controls []map[string]json.RawMessage
	_ = json.Unmarshal(fields["wire_controls"], &controls)
	for _, control := range controls {
		for _, key := range []string{"target", "values", "allow_clear"} {
			if _, present := control[key]; !present {
				return errors.New("missing account edit control member")
			}
		}
	}
	*definition = AccountEditDefinitionV1(decoded)
	return nil
}

func (request *ProviderEditRequestV1) UnmarshalJSON(raw []byte) error {
	type plain ProviderEditRequestV1
	var decoded plain
	if err := decodeAccountCreateJSON(raw, &decoded, false, "contribution_id", "expected_package_sha256", "expected_definition_sha256", "expected_runtime_generation", "expected_state_sha256", "changes"); err != nil {
		return errors.New("invalid provider edit input")
	}
	*request = ProviderEditRequestV1(decoded)
	return nil
}
