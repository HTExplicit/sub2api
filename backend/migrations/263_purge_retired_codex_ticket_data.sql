-- Purge the data of the retired Codex route acquisition ("ticket") feature.
-- The previous release removed every reader and writer of it, so nothing in
-- the application reads what this migration deletes. Evidence names the writer
-- in the last source that had one (git d81cd8be4, paths below
-- backend/internal), or the plugin-era source where noted.
--
-- Deleted:
-- * The objects of 244/245 (no later migration altered them): the triggers
--   codex_ticket_account_claim_guard (accounts) and
--   codex_ticket_proxy_trust_guard (settings) and their functions
--   invalidate_codex_ticket_account_claims() and
--   invalidate_codex_ticket_proxy_trust() go first, because the account
--   trigger writes openai_codex_ticket_runtime and a function cannot be dropped
--   while a trigger uses it. Then the tables openai_codex_ticket_runtime (with
--   openai_codex_ticket_due_idx), openai_codex_ticket_proxy_trust and
--   openai_codex_ticket_proxy_generation.
-- * settings openai_codex_ticket_enabled and
--   openai_codex_ticket_harvest_proxy_url, the only keys the feature stored
--   (service/domain_constants.go:723,725; openai_codex_ticket_clear_proxy and
--   openai_codex_ticket_harvest_proxy_configured were a request flag and a
--   response field, never stored).
-- * accounts.extra keys codex_turn_ticket:* (service/openai_codex_ticket.go:15),
--   codex_ticket_runtime:* (service/codex_ticket_lifecycle.go:14) and
--   codex_harvest_proxy_url (service/openai_codex_ticket.go:167-171), and the
--   codexrip.codex-runtime entry of plugin_account_projections, which only the
--   Codex runtime wrote (repository/codex_native_state.go:56-66,78-113); the
--   projection map itself goes only when no other entry is left. Rows that hold
--   none of these keys are not written.
-- * sub2api_plugin_state rows of plugin codexrip.codex-runtime in the
--   namespaces tickets (codexruntime/tickets/module.go:494,509),
--   routing-demand (codexruntime/tickets/routing.go:208-227) and proxy-trust
--   (pinned harvest-proxy certificates, codexruntime/tickets/proxy.go:364,423),
--   and the route and Cookie material of codex-routing-private:
--     bundle.*  route bundles holding raw Cookie values
--               (service/codex_routing_cookies.go:45-55; keys from
--               service/codex_routing_redaction.go:21 and
--               service/codex_quality_runtime.go:536)
--     clock.*   Cookie clocks holding raw Cookie values
--               (service/codex_routing_cookies.go:38-43; keys from
--               service/codex_routing_host.go:154 and
--               service/codex_quality_runtime.go:543)
--     seen.*    first-seen index per Cookie value
--               (service/codex_routing_cookies.go:173-216)
--     validation-result.*  stored verified route probe with its bundle and
--               connection lease (service/codex_routing_validation.go:168,190-195)
-- * sub2api_plugin_leases rows of codexrip.codex-runtime in routing-account
--   (codexruntime/tickets/routing.go:45, service/codex_quality_route.go:75,
--   service/codex_routing_validation.go:136) and tickets (plugin-era per-ticket
--   leases, git 769361555 plugins/codex-runtime/internal/tickets/module.go:391).
-- * admin_account_jobs of the kinds codex_ticket_harvest, codex_ticket_stop and
--   extension_operation (service/account_job.go:19,23,24); their items follow
--   through ON DELETE CASCADE (232).
--
-- Kept in codex-routing-private:
-- * quality-run.*  quality diagnostic ledger, read by this release; it stores no
--   Cookie or STATE value (service/codex_quality_state.go:104-128).
-- * validation.*   route validation budget ledger
--   (service/codex_routing_validation.go:41-45,54-99).
-- * spent.*        send-once ledger written before probe IO, value
--   {"spent":true} (service/codex_routing_host.go:136-148).
-- * wire.*         last outbound request of an account, read by this release,
--   without its retired "cookies" member: the name/value pairs of the Cookie
--   header as sent (service/codex_routing_fingerprint.go:74-96,120,204). Its
--   other members, digests included, stay.
-- Audit logs, every other setting (codex_native_runtime_source and
-- codex_native_runtime_config included), other plugins' state, account
-- credentials and any certificate other than the harvest-proxy pins above are
-- not touched.
--
-- accounts has no updated_at trigger. Writing extra fires the long-context
-- billing triggers of 175, which keep the stored flag and therefore queue no
-- scheduler_outbox event; the scheduler rebuild at startup reloads cached
-- account payloads. Every statement is a no-op once its objects or rows are
-- gone, so the migration can run again.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DROP TRIGGER IF EXISTS codex_ticket_account_claim_guard ON accounts;
DROP TRIGGER IF EXISTS codex_ticket_proxy_trust_guard ON settings;
DROP FUNCTION IF EXISTS invalidate_codex_ticket_account_claims();
DROP FUNCTION IF EXISTS invalidate_codex_ticket_proxy_trust();
DROP TABLE IF EXISTS openai_codex_ticket_runtime;
DROP TABLE IF EXISTS openai_codex_ticket_proxy_trust;
DROP TABLE IF EXISTS openai_codex_ticket_proxy_generation;

DELETE FROM settings
WHERE key IN ('openai_codex_ticket_enabled', 'openai_codex_ticket_harvest_proxy_url');

WITH retired AS (
    SELECT a.id,
           a.extra - ARRAY(
               SELECT k
               FROM jsonb_object_keys(a.extra) AS k
               WHERE starts_with(k, 'codex_turn_ticket:')
                  OR starts_with(k, 'codex_ticket_runtime:')
                  OR k = 'codex_harvest_proxy_url'
           ) AS extra,
           COALESCE(
               jsonb_typeof(a.extra -> 'plugin_account_projections') = 'object'
               AND (a.extra -> 'plugin_account_projections') ? 'codexrip.codex-runtime',
               FALSE
           ) AS projected
    FROM accounts AS a
    WHERE jsonb_typeof(a.extra) = 'object'
)
UPDATE accounts AS a
SET extra = CASE
        WHEN NOT r.projected THEN r.extra
        WHEN (r.extra -> 'plugin_account_projections') - 'codexrip.codex-runtime' = '{}'::jsonb
            THEN r.extra - 'plugin_account_projections'
        ELSE jsonb_set(
            r.extra,
            '{plugin_account_projections}',
            (r.extra -> 'plugin_account_projections') - 'codexrip.codex-runtime'
        )
    END
FROM retired AS r
WHERE a.id = r.id
  AND (r.projected OR r.extra <> a.extra);

DELETE FROM sub2api_plugin_state
WHERE plugin_key = 'codexrip.codex-runtime'
  AND (
      namespace IN ('tickets', 'routing-demand', 'proxy-trust')
      OR (
          namespace = 'codex-routing-private'
          AND (
              starts_with(state_key, 'bundle.')
              OR starts_with(state_key, 'clock.')
              OR starts_with(state_key, 'seen.')
              OR starts_with(state_key, 'validation-result.')
          )
      )
  );

UPDATE sub2api_plugin_state
SET value = value - 'cookies',
    revision = revision + 1,
    updated_at = NOW()
WHERE plugin_key = 'codexrip.codex-runtime'
  AND namespace = 'codex-routing-private'
  AND starts_with(state_key, 'wire.')
  AND jsonb_typeof(value) = 'object'
  AND value ? 'cookies';

DELETE FROM sub2api_plugin_leases
WHERE plugin_key = 'codexrip.codex-runtime'
  AND namespace IN ('routing-account', 'tickets');

DELETE FROM admin_account_jobs
WHERE kind IN ('codex_ticket_harvest', 'codex_ticket_stop', 'extension_operation');
