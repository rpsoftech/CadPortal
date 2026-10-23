CREATE OR REPLACE VIEW v_seat_matrix_gaps AS
SELECT s.variation_id, s.id AS seat_id, s.size_mm, sc.id AS stone_code_id, sc.code AS stone_code
FROM variation_stone_seats s
CROSS JOIN stone_codes sc
LEFT JOIN stone_size_matrix m ON m.size_mm = s.size_mm AND m.stone_code_id = sc.id
WHERE s.locked_stone_code_id IS NULL
  AND sc.is_default_baseline
  AND m.size_mm IS NULL;
DROP INDEX cad_variations_deleted_idx;
-- Soft-deleted variations would reappear as live ones: drop them first.
DELETE FROM cad_variations WHERE deleted_at IS NOT NULL;
ALTER TABLE cad_variations DROP COLUMN purge_after, DROP COLUMN deleted_at;
