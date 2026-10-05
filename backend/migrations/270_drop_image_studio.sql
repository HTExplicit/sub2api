-- Remove what Image Studio left stored. The feature is deleted and the
-- application reads nothing this migration removes. A new database only loses
-- the empty tables of 2.
--
-- 1. settings.image_tools_config, the Image Studio switch.
-- 2. image_studio_artifacts, image_studio_items and image_studio_jobs (233),
--    children first, with their rows, indexes, constraints and sequences: the
--    job history with its prompts and the records of the stored image files.
--    The foreign keys from image_studio_jobs to users and api_keys go with the
--    table and no row of users or api_keys is written, but removing them locks
--    both tables exclusively until the migration commits. The tables are
--    therefore dropped last, and lock_timeout bounds the wait for that lock.
--
-- Usage logs, Ops error logs, audit logs and plugin installations are not
-- touched. The image files Image Studio wrote under <data dir>/image-studio
-- are not in the database and stay on disk. Every statement is a no-op once
-- its row or table is gone, so the migration can run again.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DELETE FROM settings
WHERE key = 'image_tools_config';

DROP TABLE IF EXISTS image_studio_artifacts;
DROP TABLE IF EXISTS image_studio_items;
DROP TABLE IF EXISTS image_studio_jobs;
