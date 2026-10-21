-- Photos and video of a design, or of one of its variations. Originals and thumbnails live in the
-- object store; this table holds what is needed to show and manage them.
CREATE TABLE design_media (
    id           bigserial PRIMARY KEY,
    design_id    bigint NOT NULL REFERENCES master_designs(id) ON DELETE CASCADE,
    -- NULL: the media belongs to the whole design
    variation_id bigint REFERENCES cad_variations(id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('image', 'video')),
    content_type text NOT NULL,
    filename     text NOT NULL,
    size_bytes   bigint NOT NULL CHECK (size_bytes > 0),
    object_key   text NOT NULL UNIQUE,
    thumb_key    text,                       -- images only
    width        int,
    height       int,
    position     int NOT NULL,               -- order within (design, variation)
    is_cover     boolean NOT NULL DEFAULT false,
    created_by   bigint,                     -- no foreign key: kept if the user is removed
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- only a design-level photo can be the catalog cover
    CONSTRAINT design_media_cover_is_a_design_image CHECK (NOT is_cover OR (kind = 'image' AND variation_id IS NULL)),
    CONSTRAINT design_media_image_has_thumb CHECK (kind <> 'image' OR (thumb_key IS NOT NULL AND width > 0 AND height > 0))
);
CREATE INDEX design_media_design_idx ON design_media (design_id, variation_id, position, id);
CREATE UNIQUE INDEX design_media_one_cover ON design_media (design_id) WHERE is_cover;
