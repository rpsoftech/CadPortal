-- Deleted variations stay in the table (seats, base input and file references intact) until the
-- trash purge removes them, so they can be restored. Everything that lists, counts, rolls up or
-- edits variations filters on deleted_at IS NULL. The row keeps its code and seq (never reused)
-- and its CAD checksums (the unique indexes still cover it, so a restore can never collide).
ALTER TABLE cad_variations
    ADD COLUMN deleted_at timestamptz,
    ADD COLUMN purge_after timestamptz;

CREATE INDEX cad_variations_deleted_idx ON cad_variations (purge_after) WHERE deleted_at IS NOT NULL;

-- Gaps matter only for live variations.
CREATE OR REPLACE VIEW v_seat_matrix_gaps AS
SELECT s.variation_id, s.id AS seat_id, s.size_mm, sc.id AS stone_code_id, sc.code AS stone_code
FROM variation_stone_seats s
JOIN cad_variations v ON v.id = s.variation_id AND v.deleted_at IS NULL
CROSS JOIN stone_codes sc
LEFT JOIN stone_size_matrix m ON m.size_mm = s.size_mm AND m.stone_code_id = sc.id
WHERE s.locked_stone_code_id IS NULL
  AND sc.is_default_baseline
  AND m.size_mm IS NULL;
