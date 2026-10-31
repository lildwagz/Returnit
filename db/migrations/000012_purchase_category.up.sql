-- A purchase can have a category (one key out of a fixed list). The app shows the name; the server stores only the key.
-- Nullable: every existing row stays without a category. Adding a nullable column with no default does not rewrite the table.
ALTER TABLE purchases
    ADD COLUMN category VARCHAR(20),
    ADD CONSTRAINT purchases_category_known
        CHECK (category IS NULL OR category IN ('phone', 'computer', 'tv_audio', 'camera', 'appliance',
                                               'kitchen', 'vehicle', 'tools', 'furniture', 'wearable', 'other'));
