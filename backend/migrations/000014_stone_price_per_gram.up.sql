-- A third way to price a stone: per gram of stone weight (grams = carats x 0.200). Each matrix row still
-- names one basis in rate_basis and only that price is used for costing.
ALTER TABLE stone_size_matrix
    ADD COLUMN rate_per_g numeric(12,2) CHECK (rate_per_g >= 0);

ALTER TABLE stone_size_matrix DROP CONSTRAINT stone_size_matrix_rate_basis_check;
ALTER TABLE stone_size_matrix
    ADD CONSTRAINT stone_size_matrix_rate_basis_check CHECK (rate_basis IN ('pc', 'ct', 'g'));
