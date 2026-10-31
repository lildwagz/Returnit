-- Back to one receipt per purchase. Purchases that now have several keep only their oldest receipt: the others are
-- deleted here, and the receipts_queue_delete trigger queues their files for removal. This loses data on purpose.
SET LOCAL lock_timeout = '3s';
DELETE FROM receipts r
USING (
    SELECT id, row_number() OVER (PARTITION BY purchase_id ORDER BY uploaded_at, id) AS n FROM receipts
) ranked
WHERE r.id = ranked.id AND ranked.n > 1;
ALTER TABLE receipts ADD CONSTRAINT receipts_purchase_id_key UNIQUE (purchase_id);
DROP INDEX idx_receipts_purchase;
