-- Storage metadata for the two CAD files per variation. SHA-256 columns and their
-- unique indexes already exist (000001); the hash is of the original bytes.
ALTER TABLE cad_variations
    ADD COLUMN file_3dm_key        text,
    ADD COLUMN file_3dm_name       text,
    ADD COLUMN file_3dm_size_bytes bigint CHECK (file_3dm_size_bytes >= 0),
    ADD COLUMN file_stl_key        text,
    ADD COLUMN file_stl_name       text,
    ADD COLUMN file_stl_size_bytes bigint CHECK (file_stl_size_bytes >= 0);

-- A stored file always carries its hash, key and name together.
ALTER TABLE cad_variations
    ADD CONSTRAINT cad_variations_3dm_complete CHECK (
        (file_3dm_sha256 IS NULL) = (file_3dm_key IS NULL)
        AND (file_3dm_sha256 IS NULL) = (file_3dm_name IS NULL)),
    ADD CONSTRAINT cad_variations_stl_complete CHECK (
        (file_stl_sha256 IS NULL) = (file_stl_key IS NULL)
        AND (file_stl_sha256 IS NULL) = (file_stl_name IS NULL));
