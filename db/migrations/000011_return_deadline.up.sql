-- A purchase can have a return deadline (the last day the shop takes the product back), separate from the warranty.
-- Reminders for it are kept in the same notifications table, told apart by `kind`.
ALTER TABLE purchases
    ADD COLUMN return_deadline DATE,
    ADD CONSTRAINT purchases_return_deadline_not_before_purchase
        CHECK (return_deadline IS NULL OR return_deadline >= purchase_date);

-- For the planner: purchases whose return deadline is close.
CREATE INDEX idx_purchases_return_deadline ON purchases (return_deadline) WHERE return_deadline IS NOT NULL;

-- 'warranty': the end of the warranty (what every existing row is). 'return': the return deadline.
-- For both, notifications.warranty_end_date holds the date the reminder was planned for.
ALTER TABLE notifications
    ADD COLUMN kind VARCHAR(20) NOT NULL DEFAULT 'warranty' CHECK (kind IN ('warranty', 'return'));
