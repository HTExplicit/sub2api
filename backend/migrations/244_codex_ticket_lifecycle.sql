-- Ticket blobs remain in accounts.extra. This table only owns scheduling,
-- one-shot execution claims and safe result metadata.
CREATE TABLE IF NOT EXISTS openai_codex_ticket_runtime (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model VARCHAR(256) NOT NULL,
    identity TEXT NOT NULL,
    phase VARCHAR(24) NOT NULL DEFAULT 'idle',
    next_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    lease_id TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    operation TEXT NOT NULL DEFAULT '',
    job_id BIGINT NOT NULL DEFAULT 0,
    manual_was_enrolled BOOLEAN NOT NULL DEFAULT FALSE,
    last_attempt_at TIMESTAMPTZ,
    last_result JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(account_id, model)
);
CREATE INDEX IF NOT EXISTS openai_codex_ticket_due_idx
    ON openai_codex_ticket_runtime(next_at)
    WHERE phase IN ('ready', 'retry');

-- Public certificates only. Keyed by the full configured proxy identity and
-- the fixed ChatGPT target; never added to the system or business root pool.
CREATE TABLE IF NOT EXISTS openai_codex_ticket_proxy_trust (
    proxy_key TEXT PRIMARY KEY,
    certificates JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Invalidate in-flight claims at the moment an account becomes unavailable or
-- changes principal, even if it is re-enabled before the old request finishes.
CREATE OR REPLACE FUNCTION invalidate_codex_ticket_account_claims() RETURNS TRIGGER AS $$
DECLARE principal_changed BOOLEAN;
BEGIN
    principal_changed := NEW.platform IS DISTINCT FROM OLD.platform
       OR NEW.type IS DISTINCT FROM OLD.type
       OR NEW.parent_account_id IS DISTINCT FROM OLD.parent_account_id
       OR COALESCE(NEW.credentials->>'chatgpt_account_id','') IS DISTINCT FROM COALESCE(OLD.credentials->>'chatgpt_account_id','')
       OR COALESCE(NEW.credentials->>'chatgpt_user_id','') IS DISTINCT FROM COALESCE(OLD.credentials->>'chatgpt_user_id','')
       OR COALESCE(NEW.credentials->>'organization_id','') IS DISTINCT FROM COALESCE(OLD.credentials->>'organization_id','')
       OR (COALESCE(NEW.credentials->>'chatgpt_account_id','')='' AND COALESCE(NEW.credentials->>'chatgpt_user_id','')='' AND
           COALESCE(NEW.credentials->>'email','') IS DISTINCT FROM COALESCE(OLD.credentials->>'email',''));
    IF (OLD.status = 'active' AND NEW.status <> 'active')
       OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at OR principal_changed THEN
        UPDATE openai_codex_ticket_runtime SET phase='stopped',next_at=NULL,lease_id='',lease_until=NULL,updated_at=NOW()
          WHERE account_id=NEW.id;
        -- Disablement keeps valid tickets; a principal change also drops legacy
        -- tickets that predate owner stamping, so startup cannot misattribute them.
        NEW.extra := (SELECT COALESCE(jsonb_object_agg(e.key, CASE WHEN e.key LIKE 'codex_ticket_runtime:%'
          THEN e.value || '{"phase":"stopped","next_attempt_at":null}'::jsonb ELSE e.value END), '{}'::jsonb)
          FROM jsonb_each(CASE WHEN jsonb_typeof(NEW.extra)='object' THEN NEW.extra ELSE '{}'::jsonb END) e
          WHERE NOT (principal_changed AND e.key LIKE 'codex_turn_ticket:%'));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER codex_ticket_account_claim_guard BEFORE UPDATE OF status,deleted_at,platform,type,parent_account_id,credentials ON accounts
    FOR EACH ROW EXECUTE FUNCTION invalidate_codex_ticket_account_claims();

-- Proxy trust is scoped to one configuration generation. Returning to an old
-- URL after editing settings requires a new credential-free preflight.
CREATE TABLE openai_codex_ticket_proxy_generation (id SMALLINT PRIMARY KEY CHECK(id=1), generation BIGINT NOT NULL DEFAULT 1);
INSERT INTO openai_codex_ticket_proxy_generation(id) VALUES(1);
CREATE OR REPLACE FUNCTION invalidate_codex_ticket_proxy_trust() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.key='openai_codex_ticket_harvest_proxy_url' AND NEW.value IS DISTINCT FROM OLD.value THEN
        UPDATE openai_codex_ticket_proxy_generation SET generation=generation+1 WHERE id=1;
        DELETE FROM openai_codex_ticket_proxy_trust;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER codex_ticket_proxy_trust_guard AFTER UPDATE OF value ON settings
    FOR EACH ROW EXECUTE FUNCTION invalidate_codex_ticket_proxy_trust();
