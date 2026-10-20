ALTER TABLE cad_variations
    DROP CONSTRAINT IF EXISTS cad_variations_3dm_complete,
    DROP CONSTRAINT IF EXISTS cad_variations_stl_complete,
    DROP COLUMN IF EXISTS file_3dm_key,
    DROP COLUMN IF EXISTS file_3dm_name,
    DROP COLUMN IF EXISTS file_3dm_size_bytes,
    DROP COLUMN IF EXISTS file_stl_key,
    DROP COLUMN IF EXISTS file_stl_name,
    DROP COLUMN IF EXISTS file_stl_size_bytes;
