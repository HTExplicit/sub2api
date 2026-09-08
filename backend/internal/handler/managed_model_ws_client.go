package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
)

const managedModelWSReservedSelectorMessage = "Model is not available for this group"

func managedModelWSClientFrameError(payload []byte) error {
	if !service.ContainsManagedModelSelector("application/json", payload) {
		return nil
	}
	return newOpenAIWSLocalTurnCloseError(coderws.StatusPolicyViolation, managedModelWSReservedSelectorMessage, service.ErrManagedModelRouteUnavailable)
}

// Every account, including private and Cindy accounts, reserves the internal
// selector namespace at the raw client boundary. Inspect only the input: the
// existing hook may legitimately produce an internal routing or wire model.
func composeManagedModelWSClientFrameGuard(next func(int, []byte, string) ([]byte, string, error)) func(int, []byte, string) ([]byte, string, error) {
	return func(turn int, payload []byte, fallbackModel string) ([]byte, string, error) {
		if err := managedModelWSClientFrameError(payload); err != nil {
			return nil, "", err
		}
		if next != nil {
			return next(turn, payload, fallbackModel)
		}
		return payload, "", nil
	}
}
