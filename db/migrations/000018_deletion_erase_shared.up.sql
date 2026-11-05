-- The person's choice when they ask for account deletion: also delete the purchases they added to shared buckets
-- (default: leave them for the other members, with "added by" empty).
SET LOCAL lock_timeout = '3s';
ALTER TABLE users ADD COLUMN deletion_erase_shared BOOLEAN NOT NULL DEFAULT false;
