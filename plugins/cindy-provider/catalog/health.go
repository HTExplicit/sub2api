package catalog

import (
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"net/http"
)

func (r Registry) ClassifyResponse(in extensionv1.CindyObservedResponse) extensionv1.CindyResponseDecision {
	out := extensionv1.CindyResponseDecision{}
	if in.Status == http.StatusUnauthorized {
		out.Health = 3
		return out
	}
	if !r.Config.BalanceDetection {
		return out
	}
	budget := func(kind, code *string) bool {
		return kind != nil && code != nil && *kind == "budget_exceeded" && *code == "429"
	}
	if in.ValidJSON {
		if in.Status == http.StatusTooManyRequests && budget(in.ErrorType, in.ErrorCode) {
			out.Balance = 1
		} else if in.Status == http.StatusOK && in.EventType != nil {
			switch *in.EventType {
			case "response.failed":
				if budget(in.ResponseErrorType, in.ResponseErrorCode) {
					out.Balance = 2
				}
			case "error":
				if budget(in.ErrorType, in.ErrorCode) {
					out.Balance = 3
				}
			}
		}
	}
	if out.Balance != 0 {
		out.Health = 1
	} else if in.Status == http.StatusForbidden {
		out.Health = 2
	}
	return out
}
