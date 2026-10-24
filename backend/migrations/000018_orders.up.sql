-- Orders: a selection of catalog variations (with a quantity each) in one metal and one stone code,
-- sent to production. Each line carries an immutable snapshot (jsonb) of what the design, its weights,
-- stone BOM and CAD file fingerprints were when the line was added, so an order stays readable and
-- printable after the design is edited, soft-deleted or purged. design_id / variation_id are only
-- pointers back to the live catalog: they become NULL when the row is purged, never block a purge.
-- Orders are cancelled, never deleted. No prices are stored: the production role may read orders.

-- One counter row gives ORD-0001, ORD-0002 ... without gaps: the row lock is held until the creating
-- transaction ends, so concurrent creators queue up and a rollback hands the number back.
CREATE TABLE order_numbering (
    id       boolean PRIMARY KEY DEFAULT true CHECK (id),
    next_seq int NOT NULL DEFAULT 0
);
INSERT INTO order_numbering (id) VALUES (true);

CREATE TABLE orders (
    id            bigserial PRIMARY KEY,
    seq           int NOT NULL UNIQUE,
    number        text NOT NULL UNIQUE,
    name          text NOT NULL DEFAULT '' CHECK (length(name) <= 120),
    notes         text NOT NULL DEFAULT '' CHECK (length(notes) <= 2000),
    due_date      date,
    status        text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'placed', 'in_production', 'done', 'cancelled')),
    metal_code    text NOT NULL,
    stone_code    text NOT NULL,
    criteria      jsonb,                 -- optional, informational: the filter the selection came from
    cloned_from   bigint REFERENCES orders (id) ON DELETE SET NULL,
    created_by    bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_created_idx ON orders (created_at DESC, id DESC);
CREATE INDEX orders_status_idx ON orders (status);

CREATE TRIGGER orders_set_updated_at BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE order_lines (
    id           bigserial PRIMARY KEY,
    order_id     bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    position     int NOT NULL CHECK (position > 0),
    design_id    bigint REFERENCES master_designs (id) ON DELETE SET NULL,
    variation_id bigint REFERENCES cad_variations (id) ON DELETE SET NULL,
    quantity     int NOT NULL CHECK (quantity > 0),
    snapshot     jsonb NOT NULL,         -- shown to clients
    internal     jsonb NOT NULL,         -- storage keys for the later file export; never sent to clients
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, position)
);
CREATE INDEX order_lines_variation_idx ON order_lines (variation_id) WHERE variation_id IS NOT NULL;

CREATE TABLE order_status_history (
    id          bigserial PRIMARY KEY,
    order_id    bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    from_status text,
    to_status   text NOT NULL,
    changed_by  bigint REFERENCES users (id) ON DELETE SET NULL,
    changed_at  timestamptz NOT NULL DEFAULT now(),
    note        text NOT NULL DEFAULT '' CHECK (length(note) <= 500)
);
CREATE INDEX order_status_history_order_idx ON order_status_history (order_id, id);
