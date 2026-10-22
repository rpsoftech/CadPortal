-- Narrowing would round weights that need more than 4 decimals, so refuse rather than lose them.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM stone_size_matrix WHERE carats_per_stone <> round(carats_per_stone, 4)) THEN
        RAISE EXCEPTION 'rows hold weights with more than 4 decimals of carats; fix them before going back';
    END IF;
END $$;
ALTER TABLE stone_size_matrix ALTER COLUMN carats_per_stone TYPE numeric(8,4);
