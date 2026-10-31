-- 000015: the language an account notice is written in, taken from users.language when the notice is queued (the
-- user row is gone by the time the "erased" notice is sent).
SET LOCAL lock_timeout = '3s';
ALTER TABLE account_emails ADD COLUMN language VARCHAR(5) NOT NULL DEFAULT 'id' CHECK (language IN ('id', 'en', 'ar'));
