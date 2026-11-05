-- Reversible without loss only while no shared bucket exists: the purchases stay (user_id is still the person who added
-- each one), only the sharing goes away.
SET LOCAL lock_timeout = '3s';
SET LOCAL statement_timeout = '30s';

UPDATE notifications SET idempotency_key = regexp_replace(idempotency_key, '^return:([^:]+):[^:]+:', 'return:\1:')
WHERE idempotency_key LIKE 'return:%';
UPDATE notifications SET idempotency_key = regexp_replace(idempotency_key, '^([0-9a-f-]{36}):[0-9a-f-]{36}:', '\1:')
WHERE idempotency_key ~ '^[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9]{4}-';

DROP TRIGGER purchases_default_bucket ON purchases;
DROP FUNCTION default_purchase_bucket();
DROP TRIGGER users_personal_bucket ON users;
DROP FUNCTION create_personal_bucket();

ALTER TABLE purchases DROP COLUMN bucket_id;
DROP TABLE bucket_invites;
DROP TABLE bucket_members;
DROP TABLE buckets;
