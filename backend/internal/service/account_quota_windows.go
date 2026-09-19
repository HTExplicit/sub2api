package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// AccountQuotaWindow preserves the upstream period. In particular a 30-day
// primary window is not a seven-day window, even for legacy codex_7d storage.
type AccountQuotaWindow struct {
	ID               string         `json:"id"`
	WindowMinutes    int            `json:"window_minutes"`
	Utilization      float64        `json:"utilization"`
	ObservedAt       *time.Time     `json:"observed_at,omitempty"`
	ResetsAt         *time.Time     `json:"resets_at"`
	Expired          bool           `json:"expired"`
	RemainingSeconds int            `json:"remaining_seconds"`
	WindowStats      *WindowStats   `json:"window_stats,omitempty"`
	Estimate         *QuotaEstimate `json:"estimate,omitempty"`
}

type QuotaEstimate struct {
	Status    string   `json:"status"`
	Total     *float64 `json:"total,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
}

// UpstreamQuotaState is the shared read model for scheduling and admin UI. It
// does not rewrite a real HTTP 429 or an administrator's account status.
type UpstreamQuotaState struct {
	Blocked bool                 `json:"blocked"`
	Until   *time.Time           `json:"until"`
	Windows []AccountQuotaWindow `json:"windows"`
}

func quotaTime(value any) *time.Time {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case time.Time:
		return &v
	case *time.Time:
		return cloneTimePtr(v)
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return &parsed
		}
	}
	return nil
}

func newQuotaWindow(id string, minutes int, used float64, reset, observed *time.Time, now time.Time) AccountQuotaWindow {
	w := AccountQuotaWindow{ID: id, WindowMinutes: minutes, Utilization: used, ResetsAt: reset, ObservedAt: observed}
	if reset != nil {
		w.Expired = !now.Before(*reset)
		if !w.Expired {
			w.RemainingSeconds = int(reset.Sub(now).Seconds())
		}
	}
	return w
}

// Windows maps each actual upstream window independently. Zero-length absent
// windows are omitted. Missing reset evidence remains unknown, never now+5h/7d.
func (s *OpenAICodexUsageSnapshot) Windows(observed time.Time) []AccountQuotaWindow {
	if s == nil {
		return nil
	}
	var out []AccountQuotaWindow
	for _, item := range []struct {
		id               string
		used             *float64
		minutes, seconds *int
	}{
		{"primary", s.PrimaryUsedPercent, s.PrimaryWindowMinutes, s.PrimaryResetAfterSeconds},
		{"secondary", s.SecondaryUsedPercent, s.SecondaryWindowMinutes, s.SecondaryResetAfterSeconds},
	} {
		if item.used == nil || math.IsNaN(*item.used) || math.IsInf(*item.used, 0) || *item.used < 0 {
			continue
		}
		minutes := 0
		if item.minutes != nil {
			minutes = *item.minutes
			if minutes <= 0 && *item.used == 0 {
				continue
			}
		}
		var reset *time.Time
		if item.seconds != nil && *item.seconds > 0 {
			v := observed.Add(time.Duration(*item.seconds) * time.Second)
			reset = &v
		}
		out = append(out, newQuotaWindow(item.id, minutes, *item.used, reset, &observed, observed))
	}
	return out
}

// OpenAIQuotaWindows accepts current raw snapshots and historic normalized
// storage. Actual window_minutes always controls the period; legacy aliases are
// only used if raw observations are absent.
func OpenAIQuotaWindows(extra map[string]any, now time.Time) []AccountQuotaWindow {
	observed := quotaTime(extra["codex_usage_updated_at"])
	var out []AccountQuotaWindow
	for _, name := range []string{"primary", "secondary"} {
		if w, ok := readQuotaWindow(extra, name, 0, observed, now); ok {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		for _, item := range []struct {
			name    string
			minutes int
		}{{"5h", 300}, {"7d", 10080}} {
			if w, ok := readQuotaWindow(extra, item.name, item.minutes, observed, now); ok {
				out = append(out, w)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].WindowMinutes < out[j].WindowMinutes })
	return out
}

func readQuotaWindow(extra map[string]any, name string, legacyMinutes int, observed *time.Time, now time.Time) (AccountQuotaWindow, bool) {
	prefix := "codex_" + name + "_"
	used, ok := resolveAccountExtraNumber(extra, prefix+"used_percent")
	if !ok || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
		return AccountQuotaWindow{}, false
	}
	minutes := legacyMinutes
	if value, found := resolveAccountExtraNumber(extra, prefix+"window_minutes"); found {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 525600 {
			return AccountQuotaWindow{}, false
		}
		minutes = int(value)
		if minutes == 0 && used == 0 {
			return AccountQuotaWindow{}, false
		}
	}
	reset := quotaTime(extra[prefix+"reset_at"])
	if observed != nil && (reset == nil || !observed.Before(*reset)) {
		if seconds, found := resolveAccountExtraNumber(extra, prefix+"reset_after_seconds"); found && seconds > 0 && seconds <= 366*24*3600 {
			x := observed.Add(time.Duration(seconds) * time.Second)
			reset = &x
		}
	}
	return newQuotaWindow(name, minutes, used, reset, observed, now), true
}

func (a *Account) QuotaState(now time.Time) *UpstreamQuotaState {
	if a == nil || a.Platform != PlatformOpenAI || !a.IsOAuth() {
		return nil
	}
	windows := OpenAIQuotaWindows(a.Extra, now)
	state := &UpstreamQuotaState{Windows: windows}
	unknown := false
	for _, w := range windows {
		if w.Expired || w.Utilization < 100 {
			continue
		}
		state.Blocked = true
		if w.ResetsAt == nil {
			unknown = true
		} else if state.Until == nil || w.ResetsAt.After(*state.Until) {
			state.Until = cloneTimePtr(w.ResetsAt)
		}
	}
	if block, ok := a.Extra["openai_quota_exhausted"].(map[string]any); ok {
		observed := quotaTime(block["observed_at"])
		until := quotaTime(block["reset_at"])
		recovered := len(windows) > 0
		for _, window := range windows {
			if observed == nil || window.ObservedAt == nil || !window.ObservedAt.After(*observed) || window.Expired || window.Utilization >= 100 {
				recovered = false
			}
		}
		if !recovered && (until == nil || now.Before(*until)) {
			state.Blocked = true
			if until == nil {
				unknown = true
			} else if state.Until == nil || until.After(*state.Until) {
				state.Until = until
			}
		}
	}
	if unknown {
		state.Until = nil
	}
	if len(windows) == 0 && !state.Blocked {
		return nil
	}
	return state
}

func persistOpenAIQuotaClassification(ctx context.Context, repo AccountRepository, account *Account, classification openAIOAuth429Classification, now time.Time) error {
	if repo == nil || account == nil || classification.Disposition == openAIOAuth429Transient {
		return nil
	}
	block := map[string]any{"observed_at": now.UTC().Format(time.RFC3339Nano), "window": classification.Window}
	if classification.ResetAt != nil {
		block["reset_at"] = classification.ResetAt.UTC().Format(time.RFC3339Nano)
	}
	updates := map[string]any{"openai_quota_exhausted": block}
	mergeAccountExtra(account, updates)
	return repo.UpdateExtra(ctx, account.ID, updates)
}

func (w AccountQuotaWindow) StatsStart() *time.Time {
	if w.ResetsAt == nil || w.WindowMinutes <= 0 || w.Expired {
		return nil
	}
	start := w.ResetsAt.Add(-time.Duration(w.WindowMinutes) * time.Minute)
	return &start
}

func (w AccountQuotaWindow) PeriodKey() string {
	if w.ResetsAt == nil || w.WindowMinutes <= 0 {
		return ""
	}
	return fmt.Sprintf("%d/%s", w.WindowMinutes, w.ResetsAt.UTC().Format(time.RFC3339))
}
