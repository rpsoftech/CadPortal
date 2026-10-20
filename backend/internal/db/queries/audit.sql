-- name: InsertAuditEntry :exec
INSERT INTO audit_log (user_id, user_email, action, entity_type, entity_id, status, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAudit :many
-- action_prefix is pre-escaped by the service with '!' as the LIKE escape character.
SELECT id, at, user_id, user_email, action, entity_type, entity_id, status, detail,
       count(*) OVER ()::bigint AS total_count
FROM audit_log
WHERE (sqlc.narg('user_id')::bigint IS NULL OR user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('action_prefix')::text IS NULL OR action LIKE sqlc.narg('action_prefix') || '%' ESCAPE '!')
  AND (sqlc.narg('from_at')::timestamptz IS NULL OR at >= sqlc.narg('from_at'))
  AND (sqlc.narg('to_at')::timestamptz IS NULL OR at < sqlc.narg('to_at'))
ORDER BY at DESC, id DESC
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: PruneAudit :execrows
DELETE FROM audit_log WHERE at < $1;
