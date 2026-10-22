-- Stone weights come from supplier lists in grams per 1000 pieces with two decimals (1.81 g per 1000 is
-- 0.00181 g a stone, 0.00905 ct), and carat weights in the trade are quoted to seven or eight decimals. A
-- weight per stone is now kept to 8 decimals of carats. Totals are still rounded to 4 decimals of carats
-- when they are stored.
ALTER TABLE stone_size_matrix ALTER COLUMN carats_per_stone TYPE numeric(16,8);
