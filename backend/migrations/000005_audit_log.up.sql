-- Append-only record of who did what. It stores no request bodies, so secrets such as
-- passwords can never end up here.
CREATE TABLE audit_log (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    -- No foreign key: history must outlive the user. The email is a snapshot taken at the
    -- time of the action (NULL user for anonymous events such as a failed login).
    user_id     bigint,
    user_email  text NOT NULL DEFAULT '',
    action      text NOT NULL CHECK (action <> ''),   -- 'metal.update', 'auth.login_failed', ...
    entity_type text NOT NULL DEFAULT '',
    entity_id   text NOT NULL DEFAULT '',
    status      int  NOT NULL,                         -- HTTP status of the request
    detail      text NOT NULL DEFAULT ''               -- short and non-sensitive (a design code, a filename)
);

CREATE INDEX audit_log_at_idx     ON audit_log (at DESC, id DESC);
CREATE INDEX audit_log_user_idx   ON audit_log (user_id, at DESC);
CREATE INDEX audit_log_action_idx ON audit_log (action text_pattern_ops, at DESC);

-- Entries are never edited. (Old rows may still be deleted by the retention job.)
CREATE FUNCTION audit_log_no_update() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log rows cannot be modified';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_log_no_update BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_update();
