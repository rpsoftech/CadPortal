-- Design codes are assigned automatically: category prefix + a zero-padded number (LR0001).
-- next_seq is the last number handed out for the category; it only ever goes up inside the
-- transaction that creates the design, so a rolled-back creation does not leave a gap.
ALTER TABLE categories ADD COLUMN next_seq integer NOT NULL DEFAULT 0 CHECK (next_seq >= 0);

-- Prefixes are upper-case letters from now on.
UPDATE categories SET code_prefix = NULLIF(upper(btrim(code_prefix)), '');

-- Continue after any code already in use (for example designs created by hand before this).
UPDATE categories c
SET next_seq = COALESCE((
    SELECT max(substring(d.code FROM length(c.code_prefix) + 1)::int)
    FROM master_designs d
    WHERE d.category_id = c.id
      AND d.code ~ ('^' || c.code_prefix || '[0-9]{1,9}$')
), 0)
WHERE c.code_prefix IS NOT NULL;
