package extensionv1

// Transport planning receives bounded request metadata, never request bodies.
type CodexTransportQuery struct {
	Method          string `json:"method"`
	Path            string `json:"path"`
	ContentType     string `json:"content_type"`
	ContentEncoding string `json:"content_encoding"`
	BodyPresent     bool   `json:"body_present"`
}

type CodexTransportPlan struct {
	Enabled  bool `json:"enabled"`
	Compress bool `json:"compress"`
}
