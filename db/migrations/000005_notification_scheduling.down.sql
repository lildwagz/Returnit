DROP INDEX IF EXISTS idx_notifications_inbox;
DROP INDEX IF EXISTS idx_notifications_due;
ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_sent_has_time,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS warranty_end_date,
    DROP COLUMN IF EXISTS offset_days,
    DROP COLUMN IF EXISTS channel;
