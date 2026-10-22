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

-- name: RequestBackup :one
-- Null requested_by is fine. ON CONFLICT: a request is already waiting, so there is nothing to add.
INSERT INTO backup_requests (requested_by) VALUES (sqlc.narg('requested_by'))
ON CONFLICT DO NOTHING
RETURNING id;

-- name: HasPendingBackupRequest :one
SELECT EXISTS (SELECT 1 FROM backup_requests WHERE picked_up_at IS NULL);

-- name: ClaimBackupRequests :execrows
UPDATE backup_requests SET picked_up_at = now() WHERE picked_up_at IS NULL;

-- name: BackupInProgress :one
-- A run that started recently and has not finished. Older unfinished rows are runs that were killed.
SELECT EXISTS (SELECT 1 FROM backup_runs WHERE finished_at IS NULL AND started_at > now() - interval '3 hours');
