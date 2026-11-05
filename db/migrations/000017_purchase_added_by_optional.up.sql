-- A purchase in a shared bucket outlives the person who added it. When that person's account is erased, the purchase
-- stays in the bucket and "added by" becomes empty ("former member"). The account purge sets it to NULL on purpose;
-- it never relies on the foreign key, whose ON DELETE CASCADE stays as the safety net for personal data.
SET LOCAL lock_timeout = '3s';
ALTER TABLE purchases ALTER COLUMN user_id DROP NOT NULL;
