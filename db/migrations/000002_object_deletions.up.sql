-- Transactional outbox for receipt files: any time a receipt row is deleted (directly, via
-- purchase delete, or via account delete cascade) or its file is replaced, the old storage key
-- is queued here in the same transaction. A worker deletes the objects and removes the rows,
-- so a crash or storage outage can never leave a deleted user's file behind.
CREATE TABLE object_deletions (
    id              BIGSERIAL PRIMARY KEY,
    storage_key     VARCHAR(512) NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_object_deletions_due ON object_deletions(next_attempt_at);

CREATE FUNCTION queue_receipt_object_deletion() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        INSERT INTO object_deletions (storage_key) VALUES (OLD.storage_key)
        ON CONFLICT (storage_key) DO NOTHING;
        RETURN OLD;
    END IF;
    -- UPDATE of storage_key: the previous file is no longer referenced.
    IF OLD.storage_key IS DISTINCT FROM NEW.storage_key THEN
        INSERT INTO object_deletions (storage_key) VALUES (OLD.storage_key)
        ON CONFLICT (storage_key) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER receipts_queue_delete
    AFTER DELETE ON receipts
    FOR EACH ROW EXECUTE FUNCTION queue_receipt_object_deletion();

CREATE TRIGGER receipts_queue_replace
    AFTER UPDATE OF storage_key ON receipts
    FOR EACH ROW EXECUTE FUNCTION queue_receipt_object_deletion();
