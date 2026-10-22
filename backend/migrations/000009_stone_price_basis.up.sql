-- A stone is priced per piece by default, or per carat when that is how it is bought. Each matrix row says
-- which, and holds a price for each basis; only the one named by rate_basis is used for costing.
ALTER TABLE stone_size_matrix
    ADD COLUMN rate_per_pc numeric(12,2) CHECK (rate_per_pc >= 0),
    ADD COLUMN rate_basis  text NOT NULL DEFAULT 'pc' CHECK (rate_basis IN ('pc', 'ct'));

-- Rows that already carry a per-carat price keep meaning exactly what they meant.
UPDATE stone_size_matrix SET rate_basis = 'ct' WHERE rate_per_ct IS NOT NULL;
