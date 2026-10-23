-- Saved costing sheets. A sheet is a frozen result: the inputs, every computed row, the rates they were
-- computed with (wastage % per metal, stone prices per matrix row) and the exact CSV that was produced.
-- It is kept as jsonb snapshots on one row rather than as live references, because the point is that a
-- later change of gold rate, wastage or stone price never alters an old sheet; a join to the masters
-- would defeat that. The summary columns exist for the list screen.
CREATE TABLE costing_sheets (
    id                  bigserial PRIMARY KEY,
    name                text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 120),
    created_by          bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    recost_of           bigint REFERENCES costing_sheets (id) ON DELETE SET NULL,
    metal_code          text NOT NULL,
    stone_code          text NOT NULL,
    gold_rate_per_g     numeric NOT NULL CHECK (gold_rate_per_g > 0),
    setting_rate_per_pc numeric NOT NULL CHECK (setting_rate_per_pc >= 0),
    design_count        int NOT NULL CHECK (design_count >= 0),
    variation_count     int NOT NULL CHECK (variation_count >= 0),
    incomplete_count    int NOT NULL DEFAULT 0 CHECK (incomplete_count >= 0),
    total               numeric NOT NULL,
    inputs              jsonb NOT NULL,   -- what was asked for (design / variation ids, rates, skip flag)
    result_rows         jsonb NOT NULL,   -- one object per costed variation
    totals              jsonb NOT NULL,
    problems            jsonb NOT NULL,   -- designs / variations that could not be costed (skip_incomplete)
    rates               jsonb NOT NULL,   -- wastage % per metal and stone prices per matrix row, as used
    csv                 text NOT NULL     -- the CSV exactly as downloaded when the sheet was made
);
CREATE INDEX costing_sheets_created_idx ON costing_sheets (created_at DESC, id DESC);
