-- An admin's "Back up now". The portal only records the wish; the backup service (which alone holds the
-- credentials and pg_dump) notices it within about half a minute and runs a backup.
CREATE TABLE backup_requests (
    id           bigserial PRIMARY KEY,
    requested_at timestamptz NOT NULL DEFAULT now(),
    requested_by bigint,                 -- no foreign key: kept if the user is removed
    picked_up_at timestamptz             -- null while waiting
);
-- At most one request waits at a time, so a double click cannot queue two backups.
CREATE UNIQUE INDEX backup_requests_one_pending ON backup_requests ((true)) WHERE picked_up_at IS NULL;
