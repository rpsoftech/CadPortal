-- name: ListCategories :many
SELECT * FROM categories ORDER BY name;

-- name: CreateCategory :one
INSERT INTO categories (name, code_prefix) VALUES ($1, $2) RETURNING *;

-- name: ListTags :many
SELECT * FROM tags WHERE active ORDER BY tag_group, name;

-- name: CreateTag :one
INSERT INTO tags (name, tag_group) VALUES ($1, $2) RETURNING *;

-- name: ListMetals :many
SELECT * FROM metals ORDER BY code;

-- name: ListActiveMetals :many
SELECT * FROM metals WHERE active ORDER BY code;

-- name: GetMetalByCode :one
SELECT * FROM metals WHERE code = $1;

-- name: GetMetalForUpdate :one
SELECT * FROM metals WHERE id = $1 FOR UPDATE;

-- name: UpdateMetal :one
UPDATE metals
SET specific_gravity = $2, wastage_pct = $3, is_default_input = $4, active = $5
WHERE id = $1
RETURNING *;

-- name: ClearDefaultInputMetal :exec
UPDATE metals SET is_default_input = false WHERE is_default_input AND id <> $1;

-- name: ListStoneCodes :many
SELECT * FROM stone_codes ORDER BY code;

-- name: GetBaselineStoneCode :one
SELECT * FROM stone_codes WHERE is_default_baseline;

-- name: GetStoneCodeByCode :one
SELECT * FROM stone_codes WHERE code = $1;

-- name: ListStoneMatrix :many
SELECT m.size_mm, m.stone_code_id, sc.code AS stone_code, m.sieve,
       m.carats_per_stone, m.rate_per_ct
FROM stone_size_matrix m
JOIN stone_codes sc ON sc.id = m.stone_code_id
ORDER BY sc.code, m.size_mm;

-- name: UpsertStoneMatrixRow :one
INSERT INTO stone_size_matrix (size_mm, stone_code_id, sieve, carats_per_stone, rate_per_ct)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (size_mm, stone_code_id) DO UPDATE
SET sieve = EXCLUDED.sieve,
    carats_per_stone = EXCLUDED.carats_per_stone,
    rate_per_ct = EXCLUDED.rate_per_ct
RETURNING (xmax = 0) AS inserted, size_mm, stone_code_id;

-- name: SetBaselineStoneCode :exec
UPDATE stone_codes SET is_default_baseline = (id = $1);

-- name: EnqueueRecalcJob :one
INSERT INTO weight_recalc_jobs (reason) VALUES ($1) RETURNING id;

-- name: ClaimRecalcJob :one
UPDATE weight_recalc_jobs
SET status = 'running', started_at = now()
WHERE id = (
    SELECT id FROM weight_recalc_jobs
    WHERE status = 'pending'
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: FinishRecalcJob :exec
UPDATE weight_recalc_jobs
SET status = $2, error = $3, finished_at = now()
WHERE id = $1;
