package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func fixedCodexRoutingBundleKey(ctx context.Context, query extensionv1.CodexRoutingQuery, kind string) string {
	if key := codexQualityBundleKey(ctx, kind); key != "" {
		return key
	}
	suffix := ""
	if _, validation := codexValidationFromContext(ctx); validation {
		suffix = ".validation"
	}
	return "bundle." + fmt.Sprint(query.AccountID) + "." + codexRoutingDigest(query.Model)[:32] + "." + kind + suffix
}

func codexCookieClockDeadline(clock codexRoutingCookieClock) *time.Time {
	var next time.Time
	for _, cookie := range clock.Cookies {
		if cookie.Value != "" && (next.IsZero() || cookie.ExpiresAt.Before(next)) {
			next = cookie.ExpiresAt
		}
	}
	if next.IsZero() {
		return nil
	}
	return &next
}

// Only expired raw values in this domain's private namespace are redacted.
// No row, observation, first-seen digest, tombstone or ledger is deleted.
func (h *nativeCodexHost) redactExpiredCodexRoutingMaterial(ctx context.Context) (extensionv1.Result, error) {
	if h.key != codexRuntimePluginKey || h.state == nil {
		return extensionv1.Result{}, errCodexRoutingUnavailable
	}
	rows, err := h.state.DueExtensionStates(ctx, h.key, extensionv1.DueStateRequest{Namespace: codexRoutingPrivateNamespace, Limit: 100})
	if err != nil {
		return extensionv1.Result{}, err
	}
	now := time.Now().UTC()
	redacted := 0
	for _, row := range rows {
		var raw json.RawMessage
		var next *time.Time
		switch {
		case strings.HasPrefix(row.Key, "bundle."):
			var bundle codexRoutingPrivateBundle
			if json.Unmarshal(row.Value, &bundle) != nil {
				continue
			}
			if now.Before(bundle.ExpiresAt) {
				next = &bundle.ExpiresAt
				raw = row.Value
			} else {
				for index := range bundle.Cookies {
					bundle.Cookies[index].Value = ""
				}
				bundle.Status = "expired"
				raw, _ = json.Marshal(bundle)
			}
		case strings.HasPrefix(row.Key, "clock."):
			var clock codexRoutingCookieClock
			if json.Unmarshal(row.Value, &clock) != nil {
				continue
			}
			for name, cookie := range clock.Cookies {
				if !now.Before(cookie.ExpiresAt) {
					delete(clock.Cookies, name)
				}
			}
			next = codexCookieClockDeadline(clock)
			raw, _ = json.Marshal(clock)
		default:
			continue
		}
		result, saveErr := h.state.CompareSwapExtensionState(ctx, h.key, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: row.Key, ExpectedRevision: row.Revision, Value: raw, NextAt: next})
		if saveErr == nil && result.Applied {
			redacted++
		}
	}
	raw, _ := json.Marshal(map[string]int{"redacted": redacted})
	return extensionv1.Result{Payload: raw}, nil
}
