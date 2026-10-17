ALTER TABLE reminder_preferences
    DROP CONSTRAINT IF EXISTS reminder_quiet_hours_need_timezone,
    DROP CONSTRAINT IF EXISTS reminder_quiet_hours_pair,
    DROP CONSTRAINT IF EXISTS reminder_days_before_is_array,
    DROP COLUMN IF EXISTS timezone;
