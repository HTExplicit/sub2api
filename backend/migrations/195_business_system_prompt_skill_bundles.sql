-- Adds immutable, content-addressed offline skill-bundle references to prompt
-- versions. Existing administrator-created versions remain inline. Only the
-- exact captured seed version is backfilled to the pinned reconstructed bundle.

ALTER TABLE system_prompt_template_versions
    ADD COLUMN IF NOT EXISTS composition_mode VARCHAR(32) NOT NULL DEFAULT 'inline';
ALTER TABLE system_prompt_template_versions
    ADD COLUMN IF NOT EXISTS bundle_id VARCHAR(128);
ALTER TABLE system_prompt_template_versions
    ADD COLUMN IF NOT EXISTS bundle_manifest_sha256 CHAR(64);

UPDATE system_prompt_template_versions AS v
SET composition_mode = 'offline_bundle',
    bundle_id = 'moxinggang-reverse-skill',
    bundle_manifest_sha256 = '22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7'
FROM system_prompt_templates AS t
WHERE t.id = v.template_id
  AND t.slug = 'moxinggang_reverse_skill'
  AND t.is_seed = TRUE
  AND v.version = 1
  AND v.sha256 = 'c2f0269baffa6a0eb1c9a9e15df815a6582ae6a615bc51d64b7cc5342b5efcb8'
  AND v.byte_length = 7098
  AND v.composition_mode = 'inline'
  AND v.bundle_id IS NULL
  AND v.bundle_manifest_sha256 IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'system_prompt_template_versions_composition'
          AND conrelid = 'system_prompt_template_versions'::regclass
    ) THEN
        ALTER TABLE system_prompt_template_versions
            ADD CONSTRAINT system_prompt_template_versions_composition CHECK (
				composition_mode IN ('inline', 'offline_bundle')
				AND (
					(composition_mode = 'inline'
						AND bundle_id IS NULL
						AND bundle_manifest_sha256 IS NULL)
					OR
					(composition_mode = 'offline_bundle'
						AND BTRIM(bundle_id) <> ''
						AND bundle_manifest_sha256 ~ '^[0-9a-f]{64}$')
				)
            );
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION protect_system_prompt_version_content()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.template_id IS DISTINCT FROM NEW.template_id
       OR OLD.version IS DISTINCT FROM NEW.version
       OR OLD.body IS DISTINCT FROM NEW.body
       OR OLD.sha256 IS DISTINCT FROM NEW.sha256
       OR OLD.byte_length IS DISTINCT FROM NEW.byte_length
       OR OLD.composition_mode IS DISTINCT FROM NEW.composition_mode
       OR OLD.bundle_id IS DISTINCT FROM NEW.bundle_id
       OR OLD.bundle_manifest_sha256 IS DISTINCT FROM NEW.bundle_manifest_sha256
       OR OLD.note IS DISTINCT FROM NEW.note
       OR OLD.created_by IS DISTINCT FROM NEW.created_by
       OR OLD.created_at IS DISTINCT FROM NEW.created_at THEN
        RAISE EXCEPTION 'system prompt version content is immutable';
    END IF;
    RETURN NEW;
END;
$$;
