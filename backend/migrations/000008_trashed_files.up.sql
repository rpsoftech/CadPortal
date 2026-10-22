-- Files that a user removed or replaced. The object stays in the store until purge_after, so a mistake
-- can be undone; a background job deletes it for good after that. restore holds what is needed to put it
-- back (null when it cannot be, for example because its variation was deleted).
CREATE TABLE trashed_files (
    id          bigserial PRIMARY KEY,
    kind        text NOT NULL CHECK (kind IN ('cad', 'media')),
    object_key  text NOT NULL UNIQUE,
    thumb_key   text,                         -- a photo's thumbnail goes with it
    label       text NOT NULL,                -- for people: "LR0001-V1 ring.3dm"
    restore     jsonb,
    reason      text NOT NULL,                -- removed, replaced, variation_deleted
    deleted_at  timestamptz NOT NULL DEFAULT now(),
    purge_after timestamptz NOT NULL,
    deleted_by  bigint                        -- no foreign key: kept if the user is removed
);
CREATE INDEX trashed_files_purge_idx ON trashed_files (purge_after);
