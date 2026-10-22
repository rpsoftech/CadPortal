-- name: StartBackupRun :one
INSERT INTO backup_runs DEFAULT VALUES RETURNING id;

-- name: FinishBackupRun :exec
UPDATE backup_runs
SET finished_at = now(), ok = $2, db_key = $3, db_bytes = $4, files_copied = $5, files_bytes = $6,
    files_total = $7, files_skipped = $8, error = $9
WHERE id = $1;

-- name: LastGoodBackupRun :one
SELECT * FROM backup_runs WHERE ok IS TRUE ORDER BY finished_at DESC LIMIT 1;

-- name: LastBackupRun :one
SELECT * FROM backup_runs WHERE finished_at IS NOT NULL ORDER BY finished_at DESC LIMIT 1;

-- name: ListBackupRuns :many
SELECT * FROM backup_runs ORDER BY started_at DESC LIMIT $1;
