-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, expires_at) VALUES ($1, $2, $3);

-- name: GetSessionState :one
-- What the per-request check needs, all decided by the database clock. idle_secs is the longest a session
-- may go without a request; stale says the last write is old enough to be worth refreshing.
SELECT
    (revoked_at IS NULL AND expires_at > now())                                  AS open,
    (last_seen_at > now() - (sqlc.arg(idle_secs)::float8 * interval '1 second')) AS fresh,
    (last_seen_at < now() - interval '30 seconds')                               AS stale
FROM sessions
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
-- Rows are kept a day past their expiry, then dropped when someone signs in.
DELETE FROM sessions WHERE expires_at < now() - interval '1 day';
