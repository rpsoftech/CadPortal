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
SELECT * FROM trashed_files WHERE purge_after <= now() ORDER BY purge_after LIMIT 100 FOR UPDATE SKIP LOCKED;

-- name: ListDeletedDesigns :many
SELECT d.id, d.code, d.name, d.deleted_at, c.name AS category_name,
       (SELECT count(*) FROM cad_variations v WHERE v.design_id = d.id)::int AS variation_count
FROM master_designs d JOIN categories c ON c.id = d.category_id
WHERE d.deleted_at IS NOT NULL ORDER BY d.deleted_at DESC;

-- name: RestoreDesign :execrows
UPDATE master_designs SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL;

-- name: GetDeletedDesign :one
SELECT * FROM master_designs WHERE id = $1 AND deleted_at IS NOT NULL;
