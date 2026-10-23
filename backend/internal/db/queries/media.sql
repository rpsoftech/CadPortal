-- name: CountMedia :one
-- How many items a design (variation NULL) or one variation already has; the limit is per target.
SELECT count(*)::int FROM design_media
WHERE design_id = $1 AND variation_id IS NOT DISTINCT FROM sqlc.narg('variation_id')::bigint;

-- name: NextMediaPosition :one
SELECT COALESCE(MAX(position), 0)::int + 1 FROM design_media
WHERE design_id = $1 AND variation_id IS NOT DISTINCT FROM sqlc.narg('variation_id')::bigint;

-- name: InsertMedia :one
INSERT INTO design_media (design_id, variation_id, kind, content_type, filename, size_bytes,
                          object_key, thumb_key, width, height, position, created_by)
VALUES ($1, sqlc.narg('variation_id'), $2, $3, $4, $5, $6, sqlc.narg('thumb_key'),
        sqlc.narg('width'), sqlc.narg('height'), $7, sqlc.narg('created_by'))
RETURNING *;

-- name: ListMediaByDesign :many
SELECT m.* FROM design_media m WHERE m.design_id = $1
  AND (m.variation_id IS NULL OR EXISTS (SELECT 1 FROM cad_variations v WHERE v.id = m.variation_id AND v.deleted_at IS NULL))
ORDER BY m.variation_id NULLS FIRST, m.position, m.id;

-- name: ListMediaByVariation :many
SELECT * FROM design_media WHERE variation_id = $1;

-- name: GetMedia :one
SELECT * FROM design_media WHERE id = $1;

-- name: DeleteMedia :one
DELETE FROM design_media WHERE id = $1 RETURNING *;

-- name: ClearCover :exec
UPDATE design_media SET is_cover = false WHERE design_id = $1 AND is_cover;

-- name: SetCover :exec
UPDATE design_media SET is_cover = true WHERE id = $1;

-- name: LockDesignForMedia :one
-- Serialises uploads to one design so the limit of five cannot be beaten by two at once.
SELECT id FROM master_designs WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: JobBagPhotoKey :one
-- The thumbnail for a job bag: the variation's own first photo, else the design's cover photo,
-- else the design's first photo.
SELECT thumb_key FROM design_media
WHERE design_id = $1 AND kind = 'image' AND thumb_key IS NOT NULL
  AND (variation_id = sqlc.narg('variation_id') OR variation_id IS NULL)
ORDER BY (variation_id IS NOT NULL) DESC, is_cover DESC, position, id
LIMIT 1;
