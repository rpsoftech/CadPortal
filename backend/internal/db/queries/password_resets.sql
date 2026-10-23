-- name: CreatePasswordReset :one
INSERT INTO password_resets (user_id, token_hash, delivery, created_by, expires_at)
VALUES ($1, $2, $3, sqlc.narg('created_by'), $4)
RETURNING id;

-- name: SupersedePasswordResets :exec
-- A new link replaces every older one that is still open.
UPDATE password_resets SET used_at = now() WHERE user_id = $1 AND used_at IS NULL;

-- name: GetPasswordResetForUpdate :one
SELECT * FROM password_resets WHERE token_hash = $1 FOR UPDATE;

-- name: UsePasswordReset :exec
UPDATE password_resets SET used_at = now() WHERE id = $1;

-- name: CountRecentPasswordResets :one
-- Links made for this user in the window, for throttling self-service requests.
SELECT count(*)::int FROM password_resets WHERE user_id = $1 AND delivery = 'email' AND created_at > $2;

-- name: DeleteOldPasswordResets :exec
DELETE FROM password_resets WHERE created_at < now() - interval '30 days';
