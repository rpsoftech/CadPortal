/** A category code prefix is 1 to 6 letters (LR, PS); empty means "not set yet". */
export const PREFIX_PATTERN = /^[A-Za-z]{0,6}$/;

/** The upper-cased prefix, or null when what was typed is not a valid prefix. */
export function normalizePrefix(raw: string): string | null {
  const p = raw.trim();
  return PREFIX_PATTERN.test(p) ? p.toUpperCase() : null;
}
