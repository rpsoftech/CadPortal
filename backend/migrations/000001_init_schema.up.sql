-- Shared updated_at trigger
CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ---------- Masters ----------
CREATE TABLE categories (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    code_prefix text UNIQUE,            -- open decision (RG vs LR); nullable until settled
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tags (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    tag_group  text NOT NULL CHECK (tag_group IN ('Audience','Sub-Style','Motif','Attribute')),
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tag_group, name)
);

CREATE TABLE metals (
    id                bigserial PRIMARY KEY,
    code              text NOT NULL UNIQUE,                 -- 22KY, 18KR ...
    specific_gravity  numeric(8,4) NOT NULL CHECK (specific_gravity > 0),
    wastage_pct       numeric(6,3) CHECK (wastage_pct >= 0), -- open decision; NULL until set
    is_default_input  boolean NOT NULL DEFAULT false,
    active            boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX metals_one_default_input ON metals (is_default_input) WHERE is_default_input;

CREATE TABLE stone_codes (
    id                  bigserial PRIMARY KEY,
    code                text NOT NULL UNIQUE,               -- 1HD, 2HD ...
    is_default_baseline boolean NOT NULL DEFAULT false,
    active              boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX stone_codes_one_baseline ON stone_codes (is_default_baseline) WHERE is_default_baseline;

CREATE TABLE stone_size_matrix (
    size_mm          numeric(5,2) NOT NULL CHECK (size_mm > 0),
    stone_code_id    bigint NOT NULL REFERENCES stone_codes(id) ON DELETE CASCADE,
    sieve            text,
    carats_per_stone numeric(8,4) NOT NULL CHECK (carats_per_stone >= 0),
    rate_per_ct      numeric(12,2) CHECK (rate_per_ct >= 0),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (size_mm, stone_code_id)
);

-- ---------- Designs ----------
CREATE TABLE master_designs (
    id          bigserial PRIMARY KEY,
    code        text NOT NULL UNIQUE,                       -- LR001
    name        text NOT NULL DEFAULT '',
    category_id bigint NOT NULL REFERENCES categories(id),
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX master_designs_category_idx ON master_designs (category_id) WHERE deleted_at IS NULL;

CREATE TABLE design_tags (
    design_id bigint NOT NULL REFERENCES master_designs(id) ON DELETE CASCADE,
    tag_id    bigint NOT NULL REFERENCES tags(id),
    PRIMARY KEY (design_id, tag_id)
);

CREATE TABLE cad_variations (
    id                      bigserial PRIMARY KEY,
    design_id               bigint NOT NULL REFERENCES master_designs(id) ON DELETE CASCADE,
    seq                     int NOT NULL CHECK (seq > 0),
    code                    text NOT NULL UNIQUE,           -- LR001-V1
    base_volume_mm3         numeric(14,3),
    base_ref_weight_g       numeric(10,3),
    base_ref_metal_id       bigint REFERENCES metals(id),
    file_3dm_sha256         char(64),
    file_stl_sha256         char(64),
    baseline_stone_carats   numeric(10,4) NOT NULL DEFAULT 0,
    baseline_gross_weight_g numeric(10,3),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    UNIQUE (design_id, seq),
    -- exactly one base input: volume, OR weight + reference alloy (NULL-safe)
    CONSTRAINT cad_variations_base_input CHECK (
        (base_volume_mm3 IS NOT NULL AND base_volume_mm3 > 0
            AND base_ref_weight_g IS NULL AND base_ref_metal_id IS NULL)
     OR (base_volume_mm3 IS NULL
            AND base_ref_weight_g IS NOT NULL AND base_ref_weight_g > 0
            AND base_ref_metal_id IS NOT NULL)
    )
);
CREATE UNIQUE INDEX cad_variations_3dm_sha_uq ON cad_variations (file_3dm_sha256) WHERE file_3dm_sha256 IS NOT NULL;
CREATE UNIQUE INDEX cad_variations_stl_sha_uq ON cad_variations (file_stl_sha256) WHERE file_stl_sha256 IS NOT NULL;

CREATE TABLE variation_stone_seats (
    id                   bigserial PRIMARY KEY,
    variation_id         bigint NOT NULL REFERENCES cad_variations(id) ON DELETE CASCADE,
    position             int NOT NULL,
    size_mm              numeric(5,2) NOT NULL,
    stone_count          int NOT NULL CHECK (stone_count > 0),
    locked_stone_code_id bigint,
    UNIQUE (variation_id, position),
    -- a locked seat must have a matrix row; deleting the row only unlocks the seat
    FOREIGN KEY (size_mm, locked_stone_code_id)
        REFERENCES stone_size_matrix (size_mm, stone_code_id)
        ON DELETE SET NULL (locked_stone_code_id)
);

CREATE TABLE variation_metal_weights (
    variation_id    bigint NOT NULL REFERENCES cad_variations(id) ON DELETE CASCADE,
    metal_id        bigint NOT NULL REFERENCES metals(id),
    net_weight_g    numeric(10,3) NOT NULL,
    gross_weight_g  numeric(10,3),
    PRIMARY KEY (variation_id, metal_id)
);

CREATE TABLE weight_recalc_jobs (
    id          bigserial PRIMARY KEY,
    reason      text NOT NULL,        -- metal_sg | matrix_baseline | baseline_code
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed')),
    error       text,
    enqueued_at timestamptz NOT NULL DEFAULT now(),
    started_at  timestamptz,
    finished_at timestamptz
);
CREATE INDEX weight_recalc_jobs_pending_idx ON weight_recalc_jobs (id) WHERE status = 'pending';

-- Flexible seats whose size has no matrix row for the baseline code
CREATE VIEW v_seat_matrix_gaps AS
SELECT s.variation_id, s.id AS seat_id, s.size_mm, sc.id AS stone_code_id, sc.code AS stone_code
FROM variation_stone_seats s
CROSS JOIN stone_codes sc
LEFT JOIN stone_size_matrix m ON m.size_mm = s.size_mm AND m.stone_code_id = sc.id
WHERE s.locked_stone_code_id IS NULL
  AND sc.is_default_baseline
  AND m.size_mm IS NULL;

-- updated_at triggers
CREATE TRIGGER trg_categories_upd         BEFORE UPDATE ON categories         FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_tags_upd               BEFORE UPDATE ON tags               FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_metals_upd             BEFORE UPDATE ON metals             FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_stone_codes_upd        BEFORE UPDATE ON stone_codes        FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_stone_size_matrix_upd  BEFORE UPDATE ON stone_size_matrix  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_master_designs_upd     BEFORE UPDATE ON master_designs     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_cad_variations_upd     BEFORE UPDATE ON cad_variations     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
