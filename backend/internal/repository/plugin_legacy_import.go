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
	if profile == "" || profile == "cindy-provider-v1" || profile == "image-tools-v1" || profile == "admin-observability-v1" {
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
	_, hasLegacyMaterial := account.Extra["codex_turn_ticket:"+model]
	if legacy == nil && !hasLegacyMaterial {
		return nil
	}
	request, err := legacyCodexRoutingImport(account, model, legacy, time.Now().UTC())
	if err != nil {
		return err
	}
	_, err = r.CompareSwapExtensionState(ctx, plugin, request)
	return err
}

// Retired STATE values never grant access, regardless of length or shape.
// Only the persisted lifecycle can carry renewal intent across the cutover.
func legacyCodexRoutingImport(account *service.Account, model string, legacy *service.CodexTicketLifecycle, now time.Time) (extensionv1.StateRequest, error) {
	identity := service.CodexTicketAccountIdentity(account)
	phase := "stopped"
	var next, attempt *time.Time
	var lastCode, operation string
	enrolled := false
	if legacy != nil {
		phase, attempt = legacy.Phase, legacy.LastAttemptAt
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
	if phase == "ready" || phase == "retry" {
		phase, enrolled, next = "needs_cookie_verification", true, &now
	}
	if lastCode == "" || lastCode == "ticket_ready" || lastCode == "ticket_skipped" {
		lastCode = "routing_legacy_retired"
	}
	value, err := json.Marshal(map[string]any{"schema": extensionv1.CodexRoutingSchema, "phase": phase, "identity": identity, "enrolled": enrolled, "operation_id": operation, "next_attempt_at": next, "last_code": lastCode, "last_attempt_at": attempt})
	if err != nil {
		return extensionv1.StateRequest{}, err
	}
	constraint := extensionv1.SchedulingConstraint{Model: model, Effect: "deny", Reason: "ticket_missing"}
	sum := sha256.Sum256([]byte(model))
	observation := extensionv1.AccountObservation{Key: model, Kind: "codex_routing", State: phase, NextAt: next, CheckedAt: attempt, Code: lastCode}
	return extensionv1.StateRequest{Namespace: "tickets", Key: strconv.FormatInt(account.ID, 10) + "." + hex.EncodeToString(sum[:]), Value: value, NextAt: next, Projection: &extensionv1.AccountProjection{AccountID: account.ID, Identity: identity, Scheduling: []extensionv1.SchedulingConstraint{constraint}, Observations: []extensionv1.AccountObservation{observation}}}, nil
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
