-- One-time password reset links. Only a hash of the token is stored, so a copy of the database cannot be
-- used to reset anyone's password. A link works once, until expires_at, and a newer link for the same
-- user replaces the older ones.
CREATE TABLE password_resets (
    id          bigserial PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,         -- sha-256 of the token, hex
    delivery    text NOT NULL CHECK (delivery IN ('email', 'admin')),
    created_by  bigint,                       -- the admin who made the link; null when the user asked for it
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz                   -- set when it was used or replaced by a newer link
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id, created_at DESC);
