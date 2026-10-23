-- Refuse rather than silently drop prices: rows priced per gram must be re-priced (or the price
-- moved to another basis) before this can be rolled back.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM stone_size_matrix WHERE rate_basis = 'g' OR rate_per_g IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back 000014: stone_size_matrix rows use the per-gram price; change them to per piece or per carat first';
    END IF;
END $$;

ALTER TABLE stone_size_matrix DROP CONSTRAINT stone_size_matrix_rate_basis_check;
ALTER TABLE stone_size_matrix
    ADD CONSTRAINT stone_size_matrix_rate_basis_check CHECK (rate_basis IN ('pc', 'ct'));
ALTER TABLE stone_size_matrix DROP COLUMN rate_per_g;
