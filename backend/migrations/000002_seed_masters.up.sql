-- Categories are fixed by the spec. code_prefix left NULL (open decision).
INSERT INTO categories (name) VALUES
    ('Rings'), ('Earrings'), ('Pendants'), ('Pendant Sets'),
    ('Necklaces'), ('Bangles'), ('Bracelets');

-- Metals with SG from the spec fixtures. wastage_pct and is_default_input are
-- open decisions and are intentionally left unset for Admin to fill in.
INSERT INTO metals (code, specific_gravity) VALUES
    ('22KY', 17.75),
    ('18KY', 15.58),
    ('18KR', 15.25);

-- Stone codes. 1HD is the single baseline. Matrix rows are real data from Admin.
INSERT INTO stone_codes (code, is_default_baseline) VALUES
    ('1HD', true),
    ('2HD', false);
