CREATE TABLE users (
    id                bigserial PRIMARY KEY,
    email             text NOT NULL CHECK (email = lower(email) AND email <> ''),
    name              text NOT NULL DEFAULT '',
    password_hash     text NOT NULL,                  -- Argon2id PHC string
    role              text NOT NULL CHECK (role IN ('admin', 'designer', 'production')),
    active            boolean NOT NULL DEFAULT true,
    failed_attempts   int NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until      timestamptz,
    pwd_changed_at    timestamptz NOT NULL DEFAULT now(),  -- tokens issued before this are invalid
    last_login_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_uq ON users (email);

CREATE TRIGGER trg_users_upd BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();
