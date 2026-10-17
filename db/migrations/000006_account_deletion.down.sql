DROP TABLE IF EXISTS account_deletion_log;
DROP INDEX IF EXISTS idx_users_deletion_due;
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_deletion_order,
    DROP CONSTRAINT IF EXISTS users_deletion_pair,
    DROP COLUMN IF EXISTS deletion_scheduled_at,
    DROP COLUMN IF EXISTS deletion_requested_at;
