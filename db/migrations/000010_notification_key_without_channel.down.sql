-- Puts the channel back into the key of rows that were rewritten. Rows cancelled as duplicates stay cancelled.
UPDATE notifications
SET idempotency_key = idempotency_key || ':' || channel
WHERE channel IS NOT NULL
  AND idempotency_key IS NOT NULL
  AND idempotency_key !~ ':(push|email|in_app)$'
  AND idempotency_key ~ '^[0-9a-f-]{36}:[0-9]{4}-[0-9]{2}-[0-9]{2}:[0-9]+$';
