package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	proxytransport "github.com/Wei-Shaw/sub2api/pkg/extensionapi/proxy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// This is a one-time representation adapter. Lifecycle decisions run in the
// plugin after cutover; completed bundle journals never read these old tables.
func (r *pluginRepository) ImportLegacyPluginState(ctx context.Context, plugin *service.PluginInstallation, profile string, configuration json.RawMessage) error {
	var imported bool
	if err := r.db.QueryRowContext(ctx, `SELECT state_imported FROM sub2api_plugin_bootstrap WHERE plugin_key=$1`, plugin.PluginKey).Scan(&imported); err != nil {
		return err
	}
	if imported {
		return nil
	}
	if profile == "" || profile == "cindy-provider-v1" || profile == "image-tools-v1" {
		return nil
	}
	if profile != "codex-tickets-v1" {
		return errors.New("unknown legacy state profile")
	}
	var config struct {
		Models   []string `json:"models"`
		ProxyURL string   `json:"proxy_url"`
	}
	if err := json.Unmarshal(configuration, &config); err != nil {
		return err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,platform,type,credentials,extra FROM accounts WHERE deleted_at IS NULL AND platform='openai' AND type IN ('oauth','setup-token') AND parent_account_id IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	var accounts []*service.Account
	for rows.Next() {
		a := &service.Account{}
		var credentials, extra []byte
		if err := rows.Scan(&a.ID, &a.Platform, &a.Type, &credentials, &extra); err != nil {
			_ = rows.Close()
			return err
		}
		if json.Unmarshal(credentials, &a.Credentials) != nil || json.Unmarshal(extra, &a.Extra) != nil {
			_ = rows.Close()
			return fmt.Errorf("invalid legacy account JSON for %d", a.ID)
		}
		accounts = append(accounts, a)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	for _, account := range accounts {
		for _, model := range config.Models {
			if err := r.importLegacyTicket(ctx, plugin.PluginKey, account, model); err != nil {
				return err
			}
		}
	}
	if config.ProxyURL != "" {
		return r.importLegacyProxyTrust(ctx, plugin.PluginKey, config.ProxyURL)
	}
	return nil
}

func (r *pluginRepository) importLegacyTicket(ctx context.Context, plugin string, account *service.Account, model string) error {
	legacy, err := scanTicketState(r.db.QueryRowContext(ctx, `SELECT `+ticketStateColumns+` FROM openai_codex_ticket_runtime WHERE account_id=$1 AND model=$2`, account.ID, model))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ticket := ticketFromAccount(account, model)
	if legacy == nil && ticket == nil {
		return nil
	}
	identity := service.CodexTicketAccountIdentity(account)
	phase := "stopped"
	var next, expiry, attempt *time.Time
	var lastCode, operation string
	manualEnrolled := false
	if legacy != nil {
		phase, next, expiry, attempt, manualEnrolled = legacy.Phase, legacy.NextAt, legacy.ExpiresAt, legacy.LastAttemptAt, legacy.ManualWasEnrolled
		if legacy.LastResult != nil {
			lastCode = legacy.LastResult.Code
		}
		if legacy.JobID > 0 {
			operation = fmt.Sprintf("job-%d", legacy.JobID)
		} else if parts := strings.Split(legacy.Operation, ":"); len(parts) == 4 && parts[0] == "job" {
			operation = "job-" + parts[1]
		}
		if (legacy.Identity != "" && legacy.Identity != identity) || (phase != "ready" && phase != "retry" && phase != "stopped") {
			phase = "stopped"
			next = nil
			lastCode = "ticket_interrupted"
		}
	}
	if ticket != nil && (ticket.AccountID != 0 && ticket.AccountID != account.ID || ticket.Model != "" && ticket.Model != model || len(ticket.State) != 292 || !strings.HasPrefix(ticket.State, "gAAAAA") || ticket.Identity != "" && ticket.Identity != identity) {
		ticket = nil
		phase = "stopped"
		next = nil
	}
	var rawTicket any
	if ticket != nil {
		captured := ticket.CapturedAt
		if captured.IsZero() {
			captured = ticket.ExpiresAt.Add(-time.Hour)
		}
		rawTicket = map[string]any{"state": ticket.State, "account_id": account.ID, "identity": identity, "model": model, "captured_at": captured, "expires_at": ticket.ExpiresAt}
		if legacy == nil && time.Now().Before(ticket.ExpiresAt) {
			phase = "ready"
			expiry = &ticket.ExpiresAt
			due := ticket.ExpiresAt.Add(-time.Minute)
			next = &due
		}
	}
	if ticket == nil || phase == "stopped" {
		next = nil
	}
	value, err := json.Marshal(map[string]any{"phase": phase, "identity": identity, "operation_id": operation, "next_attempt_at": next, "expires_at": expiry, "manual_was_enrolled": manualEnrolled, "last_code": lastCode, "last_attempt_at": attempt, "ticket": rawTicket})
	if err != nil {
		return err
	}
	constraint := extensionv1.SchedulingConstraint{Model: model, Effect: "deny", Reason: "ticket_missing"}
	if ticket != nil && time.Now().Before(ticket.ExpiresAt) {
		constraint.Effect = "allow"
		constraint.Until = &ticket.ExpiresAt
		constraint.Reason = "ticket_ready"
	}
	sum := sha256.Sum256([]byte(model))
	observation := extensionv1.AccountObservation{Key: model, Kind: "codex_ticket", State: phase, ExpiresAt: expiry, NextAt: next, CheckedAt: attempt, Code: lastCode}
	if ticket != nil {
		observation.Count = len(ticket.State)
	}
	_, err = r.CompareSwapExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: "tickets", Key: strconv.FormatInt(account.ID, 10) + "." + hex.EncodeToString(sum[:]), Value: value, NextAt: next, Projection: &extensionv1.AccountProjection{AccountID: account.ID, Identity: identity, Scheduling: []extensionv1.SchedulingConstraint{constraint}, Observations: []extensionv1.AccountObservation{observation}}})
	return err
}

func (r *pluginRepository) importLegacyProxyTrust(ctx context.Context, plugin, raw string) error {
	normal, err := proxytransport.Normalize(raw)
	if err != nil {
		return errors.New("invalid legacy proxy configuration")
	}
	var generation int64
	if err := r.db.QueryRowContext(ctx, `SELECT generation FROM openai_codex_ticket_proxy_generation WHERE id=1`).Scan(&generation); err != nil {
		return err
	}
	oldKey := sha256.Sum256([]byte(normal + "\x00" + strconv.FormatInt(generation, 10) + "\x00chatgpt.com"))
	var value json.RawMessage
	err = r.db.QueryRowContext(ctx, `SELECT certificates FROM openai_codex_ticket_proxy_trust WHERE proxy_key=$1`, hex.EncodeToString(oldKey[:])).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(normal + "\x00chatgpt.com"))
	_, err = r.CompareSwapExtensionState(ctx, plugin, extensionv1.StateRequest{Namespace: "proxy-trust", Key: hex.EncodeToString(key[:]), Value: value})
	return err
}
