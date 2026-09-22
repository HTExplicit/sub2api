-- Keep per-capability activation intent while a bundled replacement is staged.
-- Existing completed installations are untouched; their next upgrade captures
-- current bindings before any temporary deactivation.
ALTER TABLE sub2api_plugin_bootstrap
    ADD COLUMN IF NOT EXISTS desired_bindings JSONB
    CHECK (desired_bindings IS NULL OR jsonb_typeof(desired_bindings) = 'array');
