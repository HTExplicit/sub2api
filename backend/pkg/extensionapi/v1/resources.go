package extensionv1

// ResourceGrant authorizes one named host data operation. Network addresses and
// HTTP methods come from the host registry, never from a plugin manifest.
type ResourceGrant struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	Permission string `json:"permission"`
}

type ResourceDescriptor struct {
	Retained          bool   `json:"-"`
	ResponseKind      string `json:"response_kind,omitempty"`
	AccountParam      string `json:"-"`
	AccountBodyField  string `json:"-"`
	AccountItemsField string `json:"-"`
	AccountScopeField string `json:"-"`
	// Fixed filter scope is registered by the host only for handlers whose
	// selection predicate always enforces this platform and account type.
	FilterPlatform    string `json:"-"`
	FilterAccountType string `json:"-"`
	FilterField       string `json:"-"`
	ResourceGrant
	Method    string `json:"method"`
	Path      string `json:"path"`
	Available bool   `json:"available"`
}
