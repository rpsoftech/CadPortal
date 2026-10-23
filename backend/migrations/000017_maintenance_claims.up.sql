-- One row per periodic job that only one API instance should do per interval (the uptime heartbeat).
-- Every instance tries to claim the slot; the claim is a single atomic upsert, so exactly one wins.
CREATE TABLE maintenance_claims (
    name       text PRIMARY KEY,
    claimed_at timestamptz NOT NULL
);
