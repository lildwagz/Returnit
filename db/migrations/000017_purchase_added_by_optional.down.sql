-- Reversible only without loss while no purchase has lost its adder: purchases of former members are deleted first,
-- because they cannot satisfy NOT NULL (a rollback after shared buckets were in use removes that data).
SET LOCAL lock_timeout = '3s';
DELETE FROM purchases WHERE user_id IS NULL;
ALTER TABLE purchases ALTER COLUMN user_id SET NOT NULL;
