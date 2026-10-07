-- Remove the account traffic telemetry switch from the observability settings.
--
-- settings.admin_observability_config is one JSON object of boolean switches.
-- Earlier releases stored two in it: telemetry_enabled, the switch of the
-- per-account traffic counters, and theme_enabled, the flat theme switch. The
-- counters and their switch are deleted. The reader of the row refuses a key
-- it does not know and stops startup, so the stored key goes with the switch.
--
-- Only telemetry_enabled is removed, from that one row. theme_enabled keeps
-- its stored value; the object is written back as PostgreSQL prints jsonb,
-- which the reader accepts. A row without the key is not written, a value
-- that is not a JSON object is not written either (the reader refuses it, as
-- before), and a database without the row stays without it: the flat theme is
-- then on. No other setting is touched. The counters were Redis keys that
-- expire on their own; a migration does not reach them.
--
-- An earlier image reads the missing switch as on. Saving the observability
-- settings in such an image stores the key again, and this migration is
-- recorded by then and does not run a second time: before returning to this
-- release, run the statement below by hand.
--
-- The statement is a no-op once the key is gone, so the migration can run
-- again. No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

UPDATE settings
SET value = (value::jsonb - 'telemetry_enabled')::text, updated_at = NOW()
WHERE key = 'admin_observability_config'
  AND CASE WHEN value IS JSON OBJECT THEN value::jsonb ? 'telemetry_enabled' ELSE FALSE END;
