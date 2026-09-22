package extensionv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const AccountCreateSlot = "account.create.v1"

type AccountCreateDefaultsV1 struct {
	Concurrency    int     `json:"concurrency"`
	Priority       int     `json:"priority"`
	RateMultiplier float64 `json:"rate_multiplier"`
	LoadFactor     *int    `json:"load_factor"`
	ResponsesMode  string  `json:"responses_mode"`
}

type AccountCreateCredentialUIV1 struct {
	BaseURL           string            `json:"base_url"`
	BaseURLReadonly   bool              `json:"base_url_readonly"`
	APIKeyPlaceholder string            `json:"api_key_placeholder"`
	Hint              map[string]string `json:"hint"`
	BaseURLHint       map[string]string `json:"base_url_hint,omitempty"`
	AccountTypeHint   map[string]string `json:"account_type_hint,omitempty"`
	CatalogLabel      map[string]string `json:"catalog_label,omitempty"`
	CatalogHint       map[string]string `json:"catalog_hint,omitempty"`
}

// AccountCreateDefinitionV1 is bounded manifest data. Credentials, endpoint
// normalization, generated device identity and provenance remain host-owned.
type AccountCreateDefinitionV1 struct {
	Version                int                         `json:"version"`
	Platform               string                      `json:"platform"`
	AccountType            string                      `json:"account_type"`
	CredentialProfile      string                      `json:"credential_profile"`
	CredentialUI           AccountCreateCredentialUIV1 `json:"credential_ui"`
	Defaults               AccountCreateDefaultsV1     `json:"defaults"`
	MinimumEffectiveGroups int                         `json:"minimum_effective_groups"`
	UpstreamBillingProbe   string                      `json:"upstream_billing_probe"`
	ModelEditing           string                      `json:"model_editing"`
	CatalogSource          string                      `json:"catalog_source"`
	FieldBindings          map[string]string           `json:"field_bindings"`
}

// ProviderCreateRequestV1 contains preconditions and non-secret declared inputs.
// It never selects an installation or transports ordinary account credentials.
type ProviderCreateRequestV1 struct {
	ContributionID            string            `json:"contribution_id"`
	ExpectedPackageSHA256     string            `json:"expected_package_sha256"`
	ExpectedDefinitionSHA256  string            `json:"expected_definition_sha256"`
	ExpectedRuntimeGeneration int64             `json:"expected_runtime_generation"`
	Values                    map[string]string `json:"values"`
	InheritDefaults           []string          `json:"inherit_defaults"`
}

func decodeAccountCreateJSON(raw []byte, value any, allowLoadFactorNull bool, required ...string) error {
	var shape map[string]any
	if json.Unmarshal(raw, &shape) != nil || shape == nil {
		return errors.New("invalid account create JSON")
	}
	for _, key := range required {
		if _, exists := shape[key]; !exists {
			return errors.New("missing required account create field")
		}
	}
	var check func(any, string) bool
	check = func(value any, path string) bool {
		switch typed := value.(type) {
		case nil:
			return allowLoadFactorNull && path == "defaults.load_factor"
		case map[string]any:
			for key, item := range typed {
				next := key
				if path != "" {
					next = path + "." + key
				}
				if !check(item, next) {
					return false
				}
			}
		case []any:
			for _, item := range typed {
				if !check(item, path+"[]") {
					return false
				}
			}
		}
		return true
	}
	if !check(shape, "") {
		return errors.New("invalid account create null value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid account create JSON")
	}
	return nil
}

func (definition *AccountCreateDefinitionV1) UnmarshalJSON(raw []byte) error {
	type plain AccountCreateDefinitionV1
	var decoded plain
	if err := decodeAccountCreateJSON(raw, &decoded, true, "version", "platform", "account_type", "credential_profile", "credential_ui", "defaults", "minimum_effective_groups", "upstream_billing_probe", "model_editing", "catalog_source", "field_bindings"); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var defaults map[string]json.RawMessage
	if json.Unmarshal(fields["defaults"], &defaults) != nil {
		return errors.New("invalid account create defaults")
	}
	for _, key := range []string{"concurrency", "priority", "rate_multiplier", "load_factor", "responses_mode"} {
		if _, exists := defaults[key]; !exists {
			return errors.New("missing account create default")
		}
	}
	*definition = AccountCreateDefinitionV1(decoded)
	return nil
}

func (request *ProviderCreateRequestV1) UnmarshalJSON(raw []byte) error {
	type plain ProviderCreateRequestV1
	var decoded plain
	if err := decodeAccountCreateJSON(raw, &decoded, false, "contribution_id", "expected_package_sha256", "expected_definition_sha256", "expected_runtime_generation", "values", "inherit_defaults"); err != nil {
		return errors.New("invalid provider create input")
	}
	*request = ProviderCreateRequestV1(decoded)
	return nil
}
