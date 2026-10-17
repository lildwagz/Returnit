-- Outbox for removing the login identity at the identity provider when an account is erased.
-- Queued in the same transaction as the erasure; the worker deletes the identity and then the row.
-- The subject is a pseudonymous personal identifier, so a row lives only until the identity is
-- gone (or 30 days of failures: then it is dropped and reported at ERROR level).
-- No foreign key to users on purpose: the user row is deleted in the same transaction.
CREATE TABLE identity_deletions (
    subject         VARCHAR(255) PRIMARY KEY,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_identity_deletions_due ON identity_deletions (next_attempt_at);
