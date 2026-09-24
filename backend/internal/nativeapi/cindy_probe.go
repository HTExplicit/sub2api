package nativeapi

type CindyProbeOutcome uint8

const (
	CindyProbeOther CindyProbeOutcome = iota
	CindyProbeSuccess
	CindyProbeExhausted
	CindyProbeNetworkFailure
	CindyProbeServerFailure
)

// Historical stage IDs remain stable in the host's durable job records.
type CindyProbePlan struct {
	Models          [2]string `json:"models"`
	Input           string    `json:"input"`
	MaxOutputTokens int       `json:"max_output_tokens"`
}

type CindyProbeResult struct {
	Stage     string            `json:"stage"`
	WasMarked bool              `json:"was_marked"`
	Outcome   CindyProbeOutcome `json:"outcome"`
}

type CindyProbeDecision struct {
	Action         string `json:"action"`
	Outcome        string `json:"outcome,omitempty"`
	State          string `json:"state,omitempty"`
	NetworkFailure bool   `json:"network_failure,omitempty"`
}
