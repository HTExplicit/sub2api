-- Account status controls business scheduling, not ticket preparation/renewal.
-- Preserve deletion and credential-owner invalidation, including in-flight IO.
-- Existing stopped renewals are deliberately not enrolled by this migration.
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
    IF NEW.deleted_at IS DISTINCT FROM OLD.deleted_at OR principal_changed THEN
        UPDATE openai_codex_ticket_runtime SET phase='stopped',next_at=NULL,lease_id='',lease_until=NULL,updated_at=NOW()
          WHERE account_id=NEW.id;
        NEW.extra := (SELECT COALESCE(jsonb_object_agg(e.key, CASE WHEN e.key LIKE 'codex_ticket_runtime:%'
          THEN e.value || '{"phase":"stopped","next_attempt_at":null}'::jsonb ELSE e.value END), '{}'::jsonb)
          FROM jsonb_each(CASE WHEN jsonb_typeof(NEW.extra)='object' THEN NEW.extra ELSE '{}'::jsonb END) e
          WHERE NOT (principal_changed AND e.key LIKE 'codex_turn_ticket:%'));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
