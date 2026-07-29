-- Management-only account taxonomy. Folders and tags are intentionally
-- independent from account_groups and are never read by scheduler paths.
CREATE TABLE IF NOT EXISTS account_folders (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    normalized_name VARCHAR(100) NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account_tags (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    normalized_name VARCHAR(100) NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS management_folder_id BIGINT;

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'fk_accounts_management_folder_id'
    ) THEN
        ALTER TABLE accounts
            ADD CONSTRAINT fk_accounts_management_folder_id
            FOREIGN KEY (management_folder_id)
            REFERENCES account_folders(id)
            ON DELETE RESTRICT;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS account_tag_bindings (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tag_id BIGINT NOT NULL REFERENCES account_tags(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_accounts_management_folder_id
    ON accounts(management_folder_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_account_folders_sort_name
    ON account_folders(sort_order, name);
CREATE INDEX IF NOT EXISTS idx_account_tags_sort_name
    ON account_tags(sort_order, name);
CREATE INDEX IF NOT EXISTS idx_account_tag_bindings_tag_id
    ON account_tag_bindings(tag_id);
