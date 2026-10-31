-- 000014: the language the person chose in the app, so reminders (push, e-mail, inbox) are written in it.
SET LOCAL lock_timeout = '3s';
ALTER TABLE users ADD COLUMN language VARCHAR(5) NOT NULL DEFAULT 'id' CHECK (language IN ('id', 'en', 'ar'));
