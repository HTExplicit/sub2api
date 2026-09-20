package extensionv1

// ResourceGrant authorizes one named host data operation. Network addresses and
// HTTP methods come from the host registry, never from a plugin manifest.
type ResourceGrant struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	Permission string `json:"permission"`
}

type ResourceDescriptor struct {
	AccountParam      string `json:"-"`
	AccountBodyField  string `json:"-"`
	AccountItemsField string `json:"-"`
	FilterField       string `json:"-"`
	ResourceGrant
	Method    string `json:"method"`
	Path      string `json:"path"`
	Available bool   `json:"available"`
}
