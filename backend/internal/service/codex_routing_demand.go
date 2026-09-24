package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// Scheduler demand is best-effort and asynchronous. It does not mint a grant,
// enroll an untouched account, or wait on a process RPC in candidate selection.
func (m *NativeCodexRuntime) noteCodexRoutingDemand(account *Account, model string) {
	if m == nil || !isOpenAICodexTicketAccount(account) || model == "" {
		return
	}
	observation, exists := nativeCodexAccountProjection(account).Observations[model]
	if !exists || observation.State == "stopped" || observation.State == "idle" {
		return
	}
	installation := m.metadata()
	if installation == nil {
		return
	}
	key := strconv.FormatInt(account.ID, 10) + ":" + model
	now := time.Now()
	m.codexDemandMu.Lock()
	if m.codexDemandAt == nil {
		m.codexDemandAt = map[string]time.Time{}
	}
	if last, exists := m.codexDemandAt[key]; exists && now.Sub(last) < 10*time.Second {
		m.codexDemandMu.Unlock()
		return
	}
	if len(m.codexDemandAt) >= 4096 {
		for item, at := range m.codexDemandAt {
			if now.Sub(at) > 2*time.Minute {
				delete(m.codexDemandAt, item)
			}
		}
		if len(m.codexDemandAt) >= 4096 {
			m.codexDemandMu.Unlock()
			return
		}
	}
	m.codexDemandAt[key] = now
	m.codexDemandMu.Unlock()
	id, kind, platform := account.ID, account.Type, account.Platform
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		raw, _ := json.Marshal(extensionv1.CodexRoutingDemand{AccountID: id, Model: model, Transport: "http"})
		_, _ = m.Invoke(ctx, platform, kind, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.routing.demand", AccountID: id, Payload: raw})
	}()
}
