-- A purchase can have several receipt files (the service allows up to 5). Until now receipts.purchase_id was UNIQUE.
-- The new index goes first and keeps lookups by purchase (and the ON DELETE CASCADE from purchases) indexed
-- once the unique constraint is gone. Both statements are instant at this table size.
SET LOCAL lock_timeout = '3s';
CREATE INDEX idx_receipts_purchase ON receipts (purchase_id, uploaded_at, id);
ALTER TABLE receipts DROP CONSTRAINT receipts_purchase_id_key;
