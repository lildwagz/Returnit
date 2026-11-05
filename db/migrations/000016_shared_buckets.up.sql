-- Shared buckets, step "expand" (plans/family-buckets.md). Every purchase now belongs to a bucket and a person sees the
-- purchases of the buckets they are a member of. Each person gets a personal bucket; this migration only creates that
-- one for everybody, so behaviour stays the same until the sharing routes exist. purchases.user_id stays as "added by".
-- Tables are tiny; every statement is instant. bucket_id becomes NOT NULL in a later migration, after the deploy.
SET LOCAL lock_timeout = '3s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE buckets (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(60) NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 60),
    personal   BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One personal bucket per person. Deleting the account removes it in code (account.PurgeDue), not through this FK.
CREATE UNIQUE INDEX idx_buckets_one_personal ON buckets (created_by) WHERE personal;

CREATE TABLE bucket_members (
    bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      VARCHAR(10) NOT NULL CHECK (role IN ('owner', 'member')),
    notify    BOOLEAN NOT NULL DEFAULT true,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_id, user_id)
);
-- "Which buckets is this person in" is the question behind every purchase query.
CREATE INDEX idx_bucket_members_user ON bucket_members (user_id, bucket_id);

-- Only the SHA-256 of an invite token is stored. At most one active (not revoked) invite per bucket.
CREATE TABLE bucket_invites (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bucket_id  UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_bucket_invites_active ON bucket_invites (bucket_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_bucket_invites_expires ON bucket_invites (expires_at);

ALTER TABLE purchases ADD COLUMN bucket_id UUID REFERENCES buckets(id) ON DELETE CASCADE;
CREATE INDEX idx_purchases_bucket_date ON purchases (bucket_id, purchase_date DESC);
CREATE INDEX idx_purchases_bucket_warranty_end ON purchases (bucket_id, warranty_end_date);

-- Backfill: a personal bucket and an owner membership for every user, and every purchase into its owner's bucket.
INSERT INTO buckets (name, personal, created_by) SELECT 'Personal', true, id FROM users;
INSERT INTO bucket_members (bucket_id, user_id, role) SELECT id, created_by, 'owner' FROM buckets WHERE personal;
UPDATE purchases p SET bucket_id = b.id FROM buckets b WHERE b.personal AND b.created_by = p.user_id;

-- From now on a new user gets a personal bucket, and a purchase inserted without a bucket goes into its adder's personal
-- bucket. Doing it in the database means every writer is covered, including the previous app version while it is still
-- running during a deploy.
CREATE FUNCTION create_personal_bucket() RETURNS trigger AS $$
DECLARE b UUID;
BEGIN
    INSERT INTO buckets (name, personal, created_by) VALUES ('Personal', true, NEW.id) RETURNING id INTO b;
    INSERT INTO bucket_members (bucket_id, user_id, role) VALUES (b, NEW.id, 'owner');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER users_personal_bucket AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION create_personal_bucket();

CREATE FUNCTION default_purchase_bucket() RETURNS trigger AS $$
BEGIN
    IF NEW.bucket_id IS NULL THEN
        SELECT bm.bucket_id INTO NEW.bucket_id FROM bucket_members bm JOIN buckets b ON b.id = bm.bucket_id
        WHERE bm.user_id = NEW.user_id AND b.personal;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER purchases_default_bucket BEFORE INSERT ON purchases FOR EACH ROW EXECUTE FUNCTION default_purchase_bucket();

-- A reminder key now names the person too ("<purchase>:<user>:<date>:<offset>"), because a purchase in a shared bucket
-- reminds each member and the key is unique. Pending reminders are rewritten so they are not planned a second time.
-- Old shapes: "<purchase>:<date>:<offset>" and "return:<purchase>:<date>:<offset>"; "snooze:<id>" keys stay as they are.
UPDATE notifications SET idempotency_key = regexp_replace(idempotency_key, '^return:([^:]+):', 'return:\1:' || user_id::text || ':')
WHERE idempotency_key LIKE 'return:%';
UPDATE notifications SET idempotency_key = regexp_replace(idempotency_key, '^([^:]+):', '\1:' || user_id::text || ':')
WHERE idempotency_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9]{4}-';
