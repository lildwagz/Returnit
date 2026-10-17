-- Account deletion with a grace period. The user requests deletion; the account is scheduled for
-- erasure at deletion_scheduled_at (request time + grace) and can be cancelled until then. The
-- worker hard-deletes due accounts; ON DELETE CASCADE removes purchases, receipts (their files are
-- queued for deletion by the receipts trigger), reminder preferences and notifications.
ALTER TABLE users
    ADD COLUMN deletion_requested_at TIMESTAMPTZ,
    ADD COLUMN deletion_scheduled_at TIMESTAMPTZ,
    ADD CONSTRAINT users_deletion_pair CHECK ((deletion_requested_at IS NULL) = (deletion_scheduled_at IS NULL)),
    ADD CONSTRAINT users_deletion_order CHECK (deletion_scheduled_at IS NULL OR deletion_scheduled_at >= deletion_requested_at);

CREATE INDEX idx_users_deletion_due ON users (deletion_scheduled_at) WHERE deletion_scheduled_at IS NOT NULL;

-- Minimal, PII-free record of completed erasures. After restoring a database backup, accounts
-- listed here must be deleted again (see Docs/docs/data-retention-policy.md). Holds only the
-- internal UUID and timestamps: no e-mail, name or identity subject.
CREATE TABLE account_deletion_log (
    user_id      UUID PRIMARY KEY,
    requested_at TIMESTAMPTZ NOT NULL,
    deleted_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
