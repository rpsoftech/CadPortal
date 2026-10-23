-- name: InsertCostingSheet :one
INSERT INTO costing_sheets (name, created_by, recost_of, metal_code, stone_code, gold_rate_per_g,
                            setting_rate_per_pc, design_count, variation_count, incomplete_count, total,
                            inputs, result_rows, totals, problems, rates, csv)
VALUES ($1, sqlc.narg('created_by'), sqlc.narg('recost_of'), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING id, created_at;

-- name: ListCostingSheets :many
SELECT s.id, s.name, s.created_by, COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name,
       s.created_at, s.recost_of, s.metal_code, s.stone_code, s.gold_rate_per_g, s.setting_rate_per_pc,
       s.design_count, s.variation_count, s.incomplete_count, s.total,
       count(*) OVER ()::bigint AS total_count
FROM costing_sheets s
LEFT JOIN users u ON u.id = s.created_by
ORDER BY s.created_at DESC, s.id DESC
LIMIT $1 OFFSET $2;

-- name: GetCostingSheet :one
SELECT s.*, COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name
FROM costing_sheets s
LEFT JOIN users u ON u.id = s.created_by
WHERE s.id = $1;

-- name: DeleteCostingSheet :execrows
DELETE FROM costing_sheets WHERE id = $1;
