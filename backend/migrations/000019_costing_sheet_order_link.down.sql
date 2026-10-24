DROP INDEX IF EXISTS costing_sheets_order_idx;
ALTER TABLE costing_sheets DROP COLUMN IF EXISTS order_id;
