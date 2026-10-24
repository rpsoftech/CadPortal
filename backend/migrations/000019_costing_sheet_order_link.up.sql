-- A costing sheet made from an order keeps a pointer back to it, so the order detail can list its
-- sheets. The sheet itself stays an immutable snapshot; the link is only a pointer, and becomes NULL
-- if the order row were ever removed (orders are cancelled, never deleted).
ALTER TABLE costing_sheets ADD COLUMN order_id bigint REFERENCES orders (id) ON DELETE SET NULL;
CREATE INDEX costing_sheets_order_idx ON costing_sheets (order_id, created_at DESC, id DESC) WHERE order_id IS NOT NULL;
