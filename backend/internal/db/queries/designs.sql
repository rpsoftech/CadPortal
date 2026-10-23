-- name: CreateDesign :one
INSERT INTO master_designs (code, name, category_id) VALUES ($1, $2, $3) RETURNING *;

-- name: GetDesignByID :one
SELECT * FROM master_designs WHERE id = $1 AND deleted_at IS NULL;

-- name: GetDesignByCode :one
SELECT * FROM master_designs WHERE code = $1 AND deleted_at IS NULL;

-- name: UpdateDesign :one
UPDATE master_designs SET name = $2, category_id = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteDesign :execrows
UPDATE master_designs SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: ClearDesignTags :exec
DELETE FROM design_tags WHERE design_id = $1;

-- name: AddDesignTag :exec
INSERT INTO design_tags (design_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: ListDesignTags :many
SELECT t.* FROM tags t JOIN design_tags dt ON dt.tag_id = t.id
WHERE dt.design_id = $1 ORDER BY t.tag_group, t.name;

-- name: ListDesigns :many
-- Weight filters apply to the selected metal (metal_code). A design matches if
-- at least one of its variations satisfies every weight filter.
SELECT d.*, c.name AS category_name,
    ARRAY(SELECT t.name FROM design_tags dt JOIN tags t ON t.id = dt.tag_id
          WHERE dt.design_id = d.id ORDER BY t.tag_group, t.name)::text[] AS tag_names,
    (SELECT count(*) FROM cad_variations cv WHERE cv.design_id = d.id AND cv.deleted_at IS NULL)::int AS variation_count,
    -- the cover photo (or, failing that, the first photo) for the catalog card
    (SELECT m.thumb_key FROM design_media m WHERE m.design_id = d.id AND m.kind = 'image'
       AND (m.variation_id IS NULL OR EXISTS (SELECT 1 FROM cad_variations mv WHERE mv.id = m.variation_id AND mv.deleted_at IS NULL))
     ORDER BY m.is_cover DESC, m.variation_id NULLS FIRST, m.position, m.id LIMIT 1) AS cover_thumb_key,
    count(*) OVER ()::bigint AS total_count   -- matches before LIMIT/OFFSET, for pagination
FROM master_designs d
JOIN categories c ON c.id = d.category_id
WHERE d.deleted_at IS NULL
  -- q is pre-escaped by the service with '!' as the LIKE escape character
  AND (sqlc.narg('q')::text IS NULL
       OR d.code ILIKE '%' || sqlc.narg('q') || '%' ESCAPE '!'
       OR d.name ILIKE '%' || sqlc.narg('q') || '%' ESCAPE '!')
  AND (sqlc.narg('category_id')::bigint IS NULL OR d.category_id = sqlc.narg('category_id'))
  AND (cardinality(sqlc.arg('tag_ids')::bigint[]) = 0 OR NOT EXISTS (
        SELECT 1 FROM unnest(sqlc.arg('tag_ids')::bigint[]) AS want(id)
        WHERE NOT EXISTS (SELECT 1 FROM design_tags dt WHERE dt.design_id = d.id AND dt.tag_id = want.id)))
  AND (
    (sqlc.narg('metal_code')::text IS NULL
        AND sqlc.narg('min_net_wt')::numeric IS NULL AND sqlc.narg('max_net_wt')::numeric IS NULL
        AND sqlc.narg('min_gross_wt')::numeric IS NULL AND sqlc.narg('max_gross_wt')::numeric IS NULL
        AND sqlc.narg('max_stone_cts')::numeric IS NULL)
    OR EXISTS (
        SELECT 1 FROM cad_variations v
        JOIN variation_metal_weights w ON w.variation_id = v.id
        JOIN metals m ON m.id = w.metal_id
        WHERE v.design_id = d.id AND v.deleted_at IS NULL
          AND (sqlc.narg('metal_code')::text IS NULL OR m.code = sqlc.narg('metal_code'))
          AND (sqlc.narg('min_net_wt')::numeric IS NULL OR w.net_weight_g >= sqlc.narg('min_net_wt'))
          AND (sqlc.narg('max_net_wt')::numeric IS NULL OR w.net_weight_g <= sqlc.narg('max_net_wt'))
          AND (sqlc.narg('min_gross_wt')::numeric IS NULL OR w.gross_weight_g >= sqlc.narg('min_gross_wt'))
          AND (sqlc.narg('max_gross_wt')::numeric IS NULL OR w.gross_weight_g <= sqlc.narg('max_gross_wt'))
          AND (sqlc.narg('max_stone_cts')::numeric IS NULL OR v.baseline_stone_carats <= sqlc.narg('max_stone_cts'))
    ))
ORDER BY d.code
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: NextVariationSeq :one
SELECT COALESCE(MAX(seq), 0)::int + 1 FROM cad_variations WHERE design_id = $1;

-- name: CreateVariation :one
INSERT INTO cad_variations (design_id, seq, code, base_volume_mm3, base_ref_weight_g, base_ref_metal_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetVariation :one
SELECT * FROM cad_variations WHERE id = $1 AND deleted_at IS NULL;

-- name: GetVariationForUpdate :one
SELECT * FROM cad_variations WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ListVariationsByDesign :many
SELECT * FROM cad_variations WHERE design_id = $1 AND deleted_at IS NULL ORDER BY seq;

-- name: ListAllVariationIDs :many
SELECT id FROM cad_variations WHERE deleted_at IS NULL ORDER BY id;

-- name: SoftDeleteVariation :one
-- The row keeps its seats, base input and file references so it can be restored; it is hidden
-- everywhere by deleted_at. Its code and seq stay taken (NextVariationSeq counts it).
UPDATE cad_variations SET deleted_at = now(), purge_after = sqlc.narg('purge_after')
WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: GetDeletedVariationForUpdate :one
SELECT * FROM cad_variations WHERE id = $1 AND deleted_at IS NOT NULL FOR UPDATE;

-- name: RestoreVariation :one
UPDATE cad_variations SET deleted_at = NULL, purge_after = NULL
WHERE id = $1 AND deleted_at IS NOT NULL RETURNING *;

-- name: ListDeletedVariations :many
SELECT v.id, v.code, v.deleted_at, v.purge_after, d.id AS design_id, d.code AS design_code,
       d.name AS design_name, d.deleted_at AS design_deleted_at,
       (v.file_3dm_key IS NOT NULL OR v.file_stl_key IS NOT NULL)::bool AS has_files
FROM cad_variations v JOIN master_designs d ON d.id = v.design_id
WHERE v.deleted_at IS NOT NULL ORDER BY v.deleted_at DESC, v.id DESC;

-- name: ClaimExpiredVariations :many
-- Deleted variations whose time is up. SKIP LOCKED keeps a purge from colliding with a restore.
SELECT * FROM cad_variations
WHERE deleted_at IS NOT NULL AND purge_after IS NOT NULL AND purge_after <= now()
ORDER BY purge_after LIMIT 50 FOR UPDATE SKIP LOCKED;

-- name: HardDeleteVariation :exec
-- Seats, weights and photo rows go with the row (ON DELETE CASCADE).
DELETE FROM cad_variations WHERE id = $1 AND deleted_at IS NOT NULL;

-- name: ListMediaForPurgedVariation :many
SELECT * FROM design_media WHERE variation_id = $1;

-- name: UpdateVariationBaseInput :one
UPDATE cad_variations
SET base_volume_mm3 = $2, base_ref_weight_g = $3, base_ref_metal_id = $4
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateVariationBaseline :exec
UPDATE cad_variations SET baseline_stone_carats = $2, baseline_gross_weight_g = $3 WHERE id = $1;

-- name: ListVariationSeats :many
SELECT s.id, s.variation_id, s.position, s.size_mm, s.stone_count,
       s.locked_stone_code_id, sc.code AS locked_code
FROM variation_stone_seats s
LEFT JOIN stone_codes sc ON sc.id = s.locked_stone_code_id
WHERE s.variation_id = $1
ORDER BY s.position;

-- name: ListMatrixAll :many
SELECT m.size_mm, sc.code AS stone_code, m.carats_per_stone
FROM stone_size_matrix m JOIN stone_codes sc ON sc.id = m.stone_code_id;

-- name: DeleteVariationWeights :exec
DELETE FROM variation_metal_weights WHERE variation_id = $1;

-- name: UpsertVariationWeight :exec
INSERT INTO variation_metal_weights (variation_id, metal_id, net_weight_g, gross_weight_g)
VALUES ($1, $2, $3, $4)
ON CONFLICT (variation_id, metal_id) DO UPDATE
SET net_weight_g = EXCLUDED.net_weight_g, gross_weight_g = EXCLUDED.gross_weight_g;

-- name: ListVariationWeights :many
SELECT w.variation_id, w.metal_id, m.code AS metal_code, w.net_weight_g, w.gross_weight_g
FROM variation_metal_weights w JOIN metals m ON m.id = w.metal_id
WHERE w.variation_id = $1
ORDER BY m.code;

-- name: DeleteVariationSeats :exec
DELETE FROM variation_stone_seats WHERE variation_id = $1;

-- name: InsertSeat :exec
INSERT INTO variation_stone_seats (variation_id, position, size_mm, stone_count, locked_stone_code_id)
VALUES ($1, $2, $3, $4, $5);

-- name: CloneSeats :exec
INSERT INTO variation_stone_seats (variation_id, position, size_mm, stone_count, locked_stone_code_id)
SELECT sqlc.arg('target_id')::bigint, src.position, src.size_mm, src.stone_count, src.locked_stone_code_id
FROM variation_stone_seats src WHERE src.variation_id = sqlc.arg('source_id');

-- name: MatrixRowExists :one
SELECT EXISTS (SELECT 1 FROM stone_size_matrix WHERE size_mm = $1 AND stone_code_id = $2);

-- name: SetVariationFile3dm :exec
UPDATE cad_variations
SET file_3dm_sha256 = $2, file_3dm_key = $3, file_3dm_name = $4, file_3dm_size_bytes = $5
WHERE id = $1;

-- name: SetVariationFileStl :exec
UPDATE cad_variations
SET file_stl_sha256 = $2, file_stl_key = $3, file_stl_name = $4, file_stl_size_bytes = $5
WHERE id = $1;

-- name: FindVariationCodeBySha3dm :one
SELECT code, (deleted_at IS NOT NULL)::bool AS deleted FROM cad_variations WHERE file_3dm_sha256 = $1;

-- name: FindVariationCodeByShaStl :one
SELECT code, (deleted_at IS NOT NULL)::bool AS deleted FROM cad_variations WHERE file_stl_sha256 = $1;

-- name: ListDesignWeightsForMetal :many
-- Per-variation weights of one metal for the given designs; the service folds
-- them into min/max ranges for the catalog cards.
SELECT sv.design_id, w.net_weight_g, w.gross_weight_g
FROM cad_variations sv
JOIN variation_metal_weights w ON w.variation_id = sv.id
JOIN metals m ON m.id = w.metal_id
WHERE m.code = $1 AND sv.design_id = ANY($2::bigint[]) AND sv.deleted_at IS NULL;

-- name: ClearVariationFile3dm :exec
UPDATE cad_variations
SET file_3dm_sha256 = NULL, file_3dm_key = NULL, file_3dm_name = NULL, file_3dm_size_bytes = NULL
WHERE id = $1;

-- name: ClearVariationFileStl :exec
UPDATE cad_variations
SET file_stl_sha256 = NULL, file_stl_key = NULL, file_stl_name = NULL, file_stl_size_bytes = NULL
WHERE id = $1;

-- name: ListExistingDesignCodes :many
-- Codes already taken, deleted designs included (their codes stay reserved).
SELECT code FROM master_designs WHERE code = ANY($1::text[]);

-- name: ListDesignExportRows :many
-- One row per variation, plus one row for a design that has none, in the same shape the
-- import reads. Tags are "Group:Name" joined by ';' so they never depend on a name being unique.
SELECT d.code, d.name, c.name AS category_name,
    COALESCE((SELECT string_agg(t.tag_group || ':' || t.name, ';' ORDER BY t.tag_group, t.name)
              FROM design_tags dt JOIN tags t ON t.id = dt.tag_id
              WHERE dt.design_id = d.id), '')::text AS tags,
    v.seq AS variation_seq,
    v.base_volume_mm3, v.base_ref_weight_g,
    COALESCE(m.code, '')::text AS ref_metal
FROM master_designs d
JOIN categories c ON c.id = d.category_id
LEFT JOIN cad_variations v ON v.design_id = d.id AND v.deleted_at IS NULL
LEFT JOIN metals m ON m.id = v.base_ref_metal_id
WHERE d.deleted_at IS NULL
ORDER BY d.code, v.seq NULLS FIRST;

-- name: ListAllVariationsForRecalc :many
-- Locks every variation (NO KEY UPDATE: edits of a variation wait, foreign-key checks do not) in id order,
-- so the full rebuild and a seat edit take the variation row before its weight rows, always.
SELECT * FROM cad_variations ORDER BY id FOR NO KEY UPDATE;

-- name: ListAllSeatsForRecalc :many
SELECT s.variation_id, s.size_mm, s.stone_count, sc.code AS locked_code
FROM variation_stone_seats s
LEFT JOIN stone_codes sc ON sc.id = s.locked_stone_code_id
ORDER BY s.variation_id, s.position;
