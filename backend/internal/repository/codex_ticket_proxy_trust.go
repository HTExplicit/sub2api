package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) CodexTicketProxyTrustGeneration(ctx context.Context) (string, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT generation FROM openai_codex_ticket_proxy_generation WHERE id=1`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", errors.New("ticket proxy generation unavailable")
	}
	var generation int64
	if err = rows.Scan(&generation); err != nil {
		return "", err
	}
	return strconv.FormatInt(generation, 10), nil
}

func (r *accountRepository) LoadCodexTicketProxyTrust(ctx context.Context, key string) (*service.CodexTicketProxyTrust, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT certificates FROM openai_codex_ticket_proxy_trust WHERE proxy_key=$1`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var raw []byte
	if err = rows.Scan(&raw); err != nil {
		return nil, err
	}
	var trust service.CodexTicketProxyTrust
	if err = json.Unmarshal(raw, &trust); err != nil {
		return nil, err
	}
	return &trust, nil
}

func (r *accountRepository) SaveCodexTicketProxyTrust(ctx context.Context, key string, trust *service.CodexTicketProxyTrust) error {
	if len(key) != 64 || trust == nil || len(trust.Certificates) == 0 {
		return errors.New("invalid ticket proxy trust")
	}
	raw, err := json.Marshal(trust)
	if err != nil {
		return err
	}
	_, err = r.sql.ExecContext(ctx, `INSERT INTO openai_codex_ticket_proxy_trust(proxy_key,certificates) VALUES($1,$2::jsonb) ON CONFLICT(proxy_key) DO UPDATE SET certificates=EXCLUDED.certificates,updated_at=NOW()`, key, string(raw))
	return err
}
