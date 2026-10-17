-- Columns the reminder scheduler needs to re-validate a notification right before dispatch
-- (purchase, warranty date, preferences and channel can all change after planning).
ALTER TABLE notifications
    ADD COLUMN channel           VARCHAR(20) CHECK (channel IS NULL OR channel IN ('push', 'email', 'in_app')),
    ADD COLUMN offset_days       INTEGER CHECK (offset_days IS NULL OR offset_days > 0),
    ADD COLUMN warranty_end_date DATE,
    ADD COLUMN last_error        TEXT,  -- cancellation reason
    ADD CONSTRAINT notifications_sent_has_time CHECK (status <> 'sent' OR sent_at IS NOT NULL);

-- Due-work queue for the dispatcher.
CREATE INDEX idx_notifications_due ON notifications (scheduled_at) WHERE status = 'scheduled';
-- Inbox listing.
CREATE INDEX idx_notifications_inbox ON notifications (user_id, sent_at DESC) WHERE status = 'sent';
