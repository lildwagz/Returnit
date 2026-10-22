-- A reminder is identified by purchase + reminder date + offset. The channel (push / email / in_app) used to be part
-- of the idempotency key, so switching channel made the planner create a second reminder for every reminder still
-- inside the planning window: a duplicate inbox entry whose read state was lost.
--
-- 1. Rewrite the keys of existing rows to the new form (purchase:date:offset). If several rows map to the same new key
--    (the duplicates this bug created), the best one keeps it: a sent one, a read one first, then a scheduled one.
-- 2. The other duplicates that are in the inbox (sent) are cancelled so the inbox shows each reminder once. They keep
--    their old key, so no later planning run can match them.
WITH ranked AS (
    SELECT id,
           regexp_replace(idempotency_key, ':(push|email|in_app)$', '') AS new_key,
           row_number() OVER (
               PARTITION BY regexp_replace(idempotency_key, ':(push|email|in_app)$', '')
               ORDER BY (status = 'sent') DESC, (read_at IS NOT NULL) DESC, (status = 'scheduled') DESC, created_at, id
           ) AS rn,
           status
    FROM notifications
    WHERE idempotency_key ~ ':(push|email|in_app)$'
), keep AS (
    UPDATE notifications n SET idempotency_key = r.new_key
    FROM ranked r
    WHERE n.id = r.id AND r.rn = 1
    RETURNING n.id
)
UPDATE notifications n
SET status = 'cancelled', last_error = 'duplicate reminder created by a channel change'
FROM ranked r
WHERE n.id = r.id AND r.rn > 1 AND r.status IN ('sent', 'scheduled');
