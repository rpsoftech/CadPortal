-- Stone weights come from supplier lists in grams per 1000 pieces with two decimals (1.81 g per 1000 is
-- 0.00181 g a stone, 0.00905 ct), which needs more than 4 decimal places of carats. A weight per stone is
-- now kept to 6 decimals. Totals are still rounded to 4 decimals of carats when they are stored.
ALTER TABLE stone_size_matrix ALTER COLUMN carats_per_stone TYPE numeric(12,6);
