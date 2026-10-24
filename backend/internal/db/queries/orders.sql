-- name: NextOrderNumber :one
-- Hands out the next order number. Runs inside the creating transaction: the row lock serialises
-- creators and a rollback gives the number back, so numbers have no gaps.
UPDATE order_numbering SET next_seq = next_seq + 1 RETURNING next_seq;

-- name: InsertOrder :one
INSERT INTO orders (seq, number, name, notes, due_date, status, metal_code, stone_code, criteria, cloned_from, created_by)
VALUES ($1, $2, $3, $4, sqlc.narg('due_date'), $5, $6, $7, sqlc.narg('criteria'), sqlc.narg('cloned_from'), sqlc.narg('created_by'))
RETURNING *;

-- name: InsertOrderLine :exec
INSERT INTO order_lines (order_id, position, design_id, variation_id, quantity, snapshot, internal)
VALUES ($1, $2, sqlc.narg('design_id'), sqlc.narg('variation_id'), $3, $4, $5);

-- name: DeleteOrderLines :exec
DELETE FROM order_lines WHERE order_id = $1;

-- name: ListOrderLines :many
SELECT * FROM order_lines WHERE order_id = $1 ORDER BY position;

-- name: GetOrderForUpdate :one
SELECT * FROM orders WHERE id = $1 FOR UPDATE;

-- name: GetOrderRow :one
SELECT o.*, COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name
FROM orders o LEFT JOIN users u ON u.id = o.created_by
WHERE o.id = $1;

-- name: ListOrders :many
SELECT o.id, o.number, o.name, o.due_date, o.status, o.metal_code, o.stone_code, o.cloned_from, o.created_by,
       COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name,
       o.created_at, o.updated_at,
       (SELECT count(*) FROM order_lines l WHERE l.order_id = o.id)::int AS line_count,
       (SELECT COALESCE(sum(l.quantity), 0) FROM order_lines l WHERE l.order_id = o.id)::bigint AS total_quantity,
       count(*) OVER ()::bigint AS total_count
FROM orders o LEFT JOIN users u ON u.id = o.created_by
WHERE (sqlc.arg('status')::text = '' OR o.status = sqlc.arg('status')::text)
  AND (sqlc.arg('q')::text = '' OR o.number ILIKE '%' || sqlc.arg('q')::text || '%' OR o.name ILIKE '%' || sqlc.arg('q')::text || '%')
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR o.created_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz IS NULL OR o.created_at < sqlc.narg('to_ts'))
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: UpdateOrderHeader :exec
UPDATE orders SET name = $2, notes = $3, due_date = sqlc.narg('due_date') WHERE id = $1;

-- name: TouchOrder :exec
UPDATE orders SET updated_at = now() WHERE id = $1;

-- name: SetOrderStatus :exec
UPDATE orders SET status = $2 WHERE id = $1;

-- name: InsertOrderStatusHistory :exec
INSERT INTO order_status_history (order_id, from_status, to_status, changed_by, note)
VALUES ($1, sqlc.narg('from_status'), $2, sqlc.narg('changed_by'), $3);

-- name: ListOrderStatusHistory :many
SELECT h.id, h.from_status, h.to_status, h.changed_by,
       COALESCE(NULLIF(u.name, ''), u.email, '')::text AS changed_by_name, h.changed_at, h.note
FROM order_status_history h LEFT JOIN users u ON u.id = h.changed_by
WHERE h.order_id = $1 ORDER BY h.id;

-- name: OrderCoverMedia :one
-- The photo shown for a line: the variation's own first photo, else the design's cover, else its first photo
-- (the same choice the job bag makes).
SELECT id, filename, content_type, thumb_key FROM design_media
WHERE design_id = $1 AND kind = 'image' AND thumb_key IS NOT NULL
  AND (variation_id = sqlc.narg('variation_id') OR variation_id IS NULL)
ORDER BY (variation_id IS NOT NULL) DESC, is_cover DESC, position, id
LIMIT 1;

-- name: GetCategoryName :one
SELECT name FROM categories WHERE id = $1;
