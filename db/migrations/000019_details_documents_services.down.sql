-- Removes the service history, the document kinds and the model/serial numbers (their data is lost).
SET LOCAL lock_timeout = '3s';
DROP TABLE IF EXISTS service_records;
ALTER TABLE receipts DROP COLUMN IF EXISTS kind;
ALTER TABLE purchases DROP COLUMN IF EXISTS serial_number, DROP COLUMN IF EXISTS model_number;
