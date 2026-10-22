-- One row per run of the backup service (a nightly database dump and a copy of new files to the backup
-- bucket). The portal reads the newest rows to warn an admin when backups have stopped.
CREATE TABLE backup_runs (
    id            bigserial PRIMARY KEY,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,                 -- null while running, or if the service was killed mid-run
    ok            boolean,                     -- null until finished
    db_key        text NOT NULL DEFAULT '',    -- where the dump went in the backup bucket
    db_bytes      bigint NOT NULL DEFAULT 0,
    files_copied  integer NOT NULL DEFAULT 0,  -- objects new since the last run
    files_bytes   bigint NOT NULL DEFAULT 0,
    files_total   integer NOT NULL DEFAULT 0,  -- objects in the backup after this run
    files_skipped boolean NOT NULL DEFAULT false, -- no object store configured (local development)
    error         text NOT NULL DEFAULT ''
);
CREATE INDEX backup_runs_started_idx ON backup_runs (started_at DESC);
