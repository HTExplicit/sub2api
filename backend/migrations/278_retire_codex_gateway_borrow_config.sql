-- Retire gateway borrowing without changing accounts or Pelican history.
DELETE FROM settings WHERE key = 'codex_gateway_borrow_config';
ALTER TABLE codex_gateway_borrow_test_tasks ALTER COLUMN execution_mode SET DEFAULT 'account';
