-- Outbox for the e-mails about account deletion: "deletion requested", "erasure in 24 hours" and
-- "your account was erased". It holds an e-mail address (personal data) only until the message is
-- sent: the worker deletes each row right after sending, and abandons rows older than 7 days.
-- There is deliberately no foreign key to users: the "erased" message must outlive the user row.
CREATE TABLE account_emails (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    kind            VARCHAR(20) NOT NULL CHECK (kind IN ('requested', 'reminder', 'erased')),
    to_address      TEXT NOT NULL,
    timezone        VARCHAR(64) NOT NULL DEFAULT 'Asia/Jakarta',
    erasure_at      TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind, erasure_at)
);

CREATE INDEX idx_account_emails_due ON account_emails (next_attempt_at);
