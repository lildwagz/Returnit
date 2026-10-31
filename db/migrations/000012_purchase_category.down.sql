ALTER TABLE purchases
    DROP CONSTRAINT IF EXISTS purchases_category_known,
    DROP COLUMN category;
