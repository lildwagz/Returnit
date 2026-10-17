-- Quiet hours are wall-clock times, so they need an explicit IANA timezone (never inferred
-- from the server). Also enforce shape rules at the database level.
ALTER TABLE reminder_preferences
    ADD COLUMN timezone VARCHAR(64),
    ADD CONSTRAINT reminder_days_before_is_array CHECK (jsonb_typeof(days_before) = 'array'),
    ADD CONSTRAINT reminder_quiet_hours_pair CHECK (
        (quiet_hours_start IS NULL) = (quiet_hours_end IS NULL)
    ),
    ADD CONSTRAINT reminder_quiet_hours_need_timezone CHECK (
        quiet_hours_start IS NULL OR timezone IS NOT NULL
    );
