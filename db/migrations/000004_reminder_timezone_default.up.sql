-- Default timezone is WIB. Existing rows (if any) are backfilled; the column becomes mandatory.
UPDATE reminder_preferences SET timezone = 'Asia/Jakarta' WHERE timezone IS NULL;
ALTER TABLE reminder_preferences
    ALTER COLUMN timezone SET DEFAULT 'Asia/Jakarta',
    ALTER COLUMN timezone SET NOT NULL,
    DROP CONSTRAINT IF EXISTS reminder_quiet_hours_need_timezone;
