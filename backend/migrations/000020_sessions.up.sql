-- A login is a row here, and its bearer token carries the row's id (claim "sid"). The row is what lets the
-- server end a session that has been idle too long, and end one at once on sign-out. Time is always the
-- database clock, so every API instance agrees.
CREATE TABLE sessions (
    id           text PRIMARY KEY,                         -- 128 random bits, hex
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),       -- the last authenticated request (written at most every 30 s)
    expires_at   timestamptz NOT NULL,                     -- the token's own expiry (SESSION_HOURS)
    revoked_at   timestamptz                               -- set on sign-out
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);
