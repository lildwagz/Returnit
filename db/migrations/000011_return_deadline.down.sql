-- Return reminders cannot exist without the column that records them.
DELETE FROM notifications WHERE kind = 'return';
ALTER TABLE notifications DROP COLUMN kind;
DROP INDEX IF EXISTS idx_purchases_return_deadline;
ALTER TABLE purchases
    DROP CONSTRAINT IF EXISTS purchases_return_deadline_not_before_purchase,
    DROP COLUMN return_deadline;
