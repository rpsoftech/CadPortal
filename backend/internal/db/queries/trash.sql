-- name: InsertTrashedFile :one
INSERT INTO trashed_files (kind, object_key, thumb_key, label, restore, reason, purge_after, deleted_by)
VALUES ($1, $2, sqlc.narg('thumb_key'), $3, sqlc.narg('restore'), $4, $5, sqlc.narg('deleted_by'))
ON CONFLICT (object_key) DO NOTHING
RETURNING *;

-- name: ListTrashedFiles :many
SELECT * FROM trashed_files ORDER BY deleted_at DESC, id DESC;

-- name: GetTrashedFileForUpdate :one
SELECT * FROM trashed_files WHERE id = $1 FOR UPDATE;

-- name: DeleteTrashedFile :exec
DELETE FROM trashed_files WHERE id = $1;

-- name: ClaimExpiredTrash :many
-- Rows whose time is up. SKIP LOCKED keeps a purge from waiting on, or colliding with, a restore.
-- A file that an order which is not cancelled recorded (its CAD object or cover thumbnail) is kept, so the
-- order can still be downloaded; it is claimed again once every such order is cancelled.
SELECT * FROM trashed_files t WHERE t.purge_after <= now()
  AND NOT EXISTS (
    SELECT 1 FROM order_lines l JOIN orders o ON o.id = l.order_id
    WHERE o.status <> 'cancelled'
      AND (l.internal->>'file_3dm_key' = t.object_key OR l.internal->>'file_stl_key' = t.object_key
           OR (t.thumb_key IS NOT NULL AND l.internal->>'cover_thumb_key' = t.thumb_key)))
ORDER BY t.purge_after LIMIT 100 FOR UPDATE OF t SKIP LOCKED;

-- name: ObjectKeyInOpenOrder :one
-- True when an order that is not cancelled recorded this CAD object.
SELECT EXISTS (
  SELECT 1 FROM order_lines l JOIN orders o ON o.id = l.order_id
  WHERE o.status <> 'cancelled' AND (l.internal->>'file_3dm_key' = $1::text OR l.internal->>'file_stl_key' = $1::text)
) AS held;

-- name: ListDeletedDesigns :many
SELECT d.id, d.code, d.name, d.deleted_at, c.name AS category_name,
       (SELECT count(*) FROM cad_variations v WHERE v.design_id = d.id AND v.deleted_at IS NULL)::int AS variation_count
FROM master_designs d JOIN categories c ON c.id = d.category_id
WHERE d.deleted_at IS NOT NULL ORDER BY d.deleted_at DESC;

-- name: RestoreDesign :execrows
UPDATE master_designs SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL;

-- name: GetDeletedDesign :one
SELECT * FROM master_designs WHERE id = $1 AND deleted_at IS NOT NULL;
