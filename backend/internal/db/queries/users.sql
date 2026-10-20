-- name: CreateUser :one
INSERT INTO users (email, name, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByIDForUpdate :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: ListUsers :many
SELECT * FROM users ORDER BY email;

-- name: CountActiveAdmins :one
SELECT count(*)::int FROM users WHERE role = 'admin' AND active;

-- name: UpdateUser :one
UPDATE users SET name = $2, role = $3, active = $4 WHERE id = $1 RETURNING *;

-- name: SetUserPassword :exec
UPDATE users
SET password_hash = $2, pwd_changed_at = now(), failed_attempts = 0, locked_until = NULL
WHERE id = $1;

-- name: RecordLoginFailure :one
-- Counts the failure and locks the account once the limit is reached.
UPDATE users
SET failed_attempts = failed_attempts + 1,
    locked_until = CASE WHEN failed_attempts + 1 >= sqlc.arg('max_attempts')::int
                        THEN now() + make_interval(secs => sqlc.arg('lock_seconds')::int)
                        ELSE locked_until END
WHERE id = sqlc.arg('id')
RETURNING failed_attempts, locked_until;

-- name: RecordLoginSuccess :exec
UPDATE users SET failed_attempts = 0, locked_until = NULL, last_login_at = now() WHERE id = $1;
