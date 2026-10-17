ALTER TABLE reminder_preferences
    ALTER COLUMN timezone DROP NOT NULL,
    ALTER COLUMN timezone DROP DEFAULT,
    ADD CONSTRAINT reminder_quiet_hours_need_timezone CHECK (quiet_hours_start IS NULL OR timezone IS NOT NULL);
