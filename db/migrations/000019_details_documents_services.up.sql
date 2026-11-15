-- Purchase details for warranty claims: model and serial number, documents of several kinds (receipt, warranty card,
-- manual, other) and a service history per purchase. Every table here is tiny, so the brief locks are harmless.
SET LOCAL lock_timeout = '3s';

ALTER TABLE purchases
    ADD COLUMN model_number  VARCHAR(100),
    ADD COLUMN serial_number VARCHAR(100);

-- Existing files are receipts. The legacy single-receipt API (/purchases/:id/receipt) only looks at kind = 'receipt'.
ALTER TABLE receipts
    ADD COLUMN kind VARCHAR(20) NOT NULL DEFAULT 'receipt'
        CONSTRAINT receipts_kind_check CHECK (kind IN ('receipt', 'warranty_card', 'manual', 'other'));

-- A visit to a service centre or a warranty claim. It belongs to the purchase (deleted with it) and is seen by
-- everyone who sees the purchase. added_by becomes NULL when that person's account is erased.
CREATE TABLE service_records (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purchase_id  UUID NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
    added_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    service_date DATE NOT NULL,
    place        VARCHAR(160),
    problem      VARCHAR(1000),
    result       VARCHAR(1000),
    cost         NUMERIC(12,2) CHECK (cost IS NULL OR cost >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_service_records_purchase ON service_records (purchase_id, service_date DESC, id);
