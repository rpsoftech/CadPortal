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
       m.carats_per_stone, m.rate_per_ct, m.rate_per_pc, m.rate_basis
FROM stone_size_matrix m
JOIN stone_codes sc ON sc.id = m.stone_code_id
ORDER BY sc.code, m.size_mm;

-- name: UpsertStoneMatrixRow :one
INSERT INTO stone_size_matrix (size_mm, stone_code_id, sieve, carats_per_stone, rate_per_ct, rate_per_pc, rate_basis)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (size_mm, stone_code_id) DO UPDATE
SET sieve = EXCLUDED.sieve,
    carats_per_stone = EXCLUDED.carats_per_stone,
    rate_per_ct = EXCLUDED.rate_per_ct,
    rate_per_pc = EXCLUDED.rate_per_pc,
    rate_basis = EXCLUDED.rate_basis
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

-- name: UpdateCategory :one
-- A new prefix starts counting again; codes already taken are skipped when numbers are handed out.
UPDATE categories SET name = $2, code_prefix = $3,
    next_seq = CASE WHEN code_prefix IS DISTINCT FROM $3 THEN 0 ELSE next_seq END
WHERE id = $1 RETURNING *;

-- name: ListAllTags :many
SELECT * FROM tags ORDER BY tag_group, name;

-- name: UpdateTag :one
UPDATE tags SET name = $2, tag_group = $3, active = $4 WHERE id = $1 RETURNING *;

-- name: CreateMetal :one
INSERT INTO metals (code, specific_gravity, wastage_pct, is_default_input, active)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateStoneCode :one
INSERT INTO stone_codes (code) VALUES ($1) RETURNING *;

-- name: GetStoneCodeForUpdate :one
SELECT * FROM stone_codes WHERE id = $1 FOR UPDATE;

-- name: SetStoneCodeActive :one
UPDATE stone_codes SET active = $2 WHERE id = $1 RETURNING *;

-- name: CountSeatsLockedTo :one
SELECT count(*)::int FROM variation_stone_seats
WHERE size_mm = $1 AND locked_stone_code_id = $2;

-- name: DeleteStoneMatrixRow :execrows
DELETE FROM stone_size_matrix WHERE size_mm = $1 AND stone_code_id = $2;

-- name: ListMatrixGaps :many
-- Distinct (size, code) pairs that flexible seats need at the baseline code but the
-- matrix does not have, with how many variations are affected.
SELECT g.size_mm, g.stone_code, count(DISTINCT g.variation_id)::int AS variation_count
FROM v_seat_matrix_gaps g
GROUP BY g.size_mm, g.stone_code
ORDER BY g.size_mm;

-- name: RecalcQueueStatus :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::int AS pending,
    count(*) FILTER (WHERE status = 'running')::int AS running,
    count(*) FILTER (WHERE status = 'failed')::int AS failed
FROM weight_recalc_jobs;

-- name: LastFailedRecalcError :one
SELECT COALESCE(error, '')::text FROM weight_recalc_jobs
WHERE status = 'failed' ORDER BY id DESC LIMIT 1;

-- name: NextCategoryNumber :one
-- Hands out the next design number of a category. Runs inside the creating transaction, so
-- the row lock serialises creators and a rollback gives the number back.
UPDATE categories SET next_seq = next_seq + 1 WHERE id = $1 RETURNING *;

-- name: RaiseCategoryNumber :exec
-- Keeps the counter past a code that was supplied explicitly (an import).
UPDATE categories SET next_seq = GREATEST(next_seq, $2) WHERE id = $1;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = $1;

-- name: DesignCodeTaken :one
SELECT EXISTS (SELECT 1 FROM master_designs WHERE code = $1);
