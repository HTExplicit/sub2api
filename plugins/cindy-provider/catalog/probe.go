package catalog

import (
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func balanceProbePlan() extensionv1.CindyProbePlan {
	return extensionv1.CindyProbePlan{
		Models: [2]string{"tencent/hy3", "z-ai/glm-5.3-flash"},
		Input:  "Reply OK.", MaxOutputTokens: 1,
	}
}

func decideBalanceProbe(in extensionv1.CindyProbeResult) (extensionv1.CindyProbeDecision, error) {
	if (in.Stage != "luna" && in.Stage != "terra") || in.Outcome > extensionv1.CindyProbeServerFailure {
		return extensionv1.CindyProbeDecision{}, errors.New("invalid Cindy probe result")
	}
	decision := extensionv1.CindyProbeDecision{Action: "complete", State: "inconclusive"}
	switch in.Outcome {
	case extensionv1.CindyProbeSuccess:
		if in.Stage == "luna" && in.WasMarked {
			return extensionv1.CindyProbeDecision{Action: "recover"}, nil
		}
		decision.Outcome = "success"
		if in.Stage == "luna" {
			decision.State = "healthy"
		}
	case extensionv1.CindyProbeExhausted:
		if in.Stage == "terra" {
			return extensionv1.CindyProbeDecision{Action: "exhausted"}, nil
		}
		decision.Outcome, decision.State = "exact", "luna_exact"
		if in.WasMarked {
			decision.State = "still_exhausted"
		}
	case extensionv1.CindyProbeNetworkFailure:
		decision.Outcome, decision.NetworkFailure = "network_error", true
	case extensionv1.CindyProbeServerFailure:
		decision.Outcome, decision.NetworkFailure = "server_error", true
	default:
		decision.Outcome = "other_error"
	}
	return decision, nil
}
