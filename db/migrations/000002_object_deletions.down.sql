DROP TRIGGER IF EXISTS receipts_queue_replace ON receipts;
DROP TRIGGER IF EXISTS receipts_queue_delete ON receipts;
DROP FUNCTION IF EXISTS queue_receipt_object_deletion();
DROP TABLE IF EXISTS object_deletions;
