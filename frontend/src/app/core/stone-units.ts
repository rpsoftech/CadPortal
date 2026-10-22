/** One carat is 0.200 g. */

/**
 * Carats for a weight in grams (carats = grams x 5), as an exact decimal string without trailing
 * zeros, or null when the input is not a plain non-negative decimal. Integer arithmetic only: 0.0014 g
 * is exactly 0.007 ct, never 0.0070000001.
 */
export function gramsToCarats(grams: string | null | undefined): string | null {
  const m = /^(\d+)(?:\.(\d+))?$/.exec((grams ?? '').trim());
  if (!m) return null;
  const frac = m[2] ?? '';
  const n = (BigInt(m[1] + frac) * 5n).toString().padStart(frac.length + 1, '0');
  const int = n.slice(0, n.length - frac.length);
  const dec = n.slice(n.length - frac.length).replace(/0+$/, '');
  return dec ? `${int}.${dec}` : int;
}

/** The number of decimal places of a plain decimal string (0 when there is no fraction). */
export function decimalPlaces(value: string): number {
  return (value.split('.')[1] ?? '').length;
}

/** Carats are stored with 4 decimal places. */
export const CARAT_DP = 4;

export type WeightCheck = { ok: true; carats: string } | { ok: false; error: string };

/**
 * Turns a weight per stone typed in grams or carats into the carats that are stored. A weight that
 * does not fit in 4 decimal places of carats is refused, never rounded: grams allow 5 decimals when
 * the last one is 0 or 5 (0.00035 g is 0.00175 ct, which is too fine; 0.0003 g is 0.0015 ct).
 */
export function weightToCarats(value: string, unit: 'g' | 'ct'): WeightCheck {
  const v = value.trim();
  if (!/^\d+(\.\d+)?$/.test(v)) return { ok: false, error: `"${v}" is not a number` };
  const carats =
    unit === 'ct' ? v.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '') : gramsToCarats(v);
  if (carats === null) return { ok: false, error: `"${v}" is not a number` };
  if (decimalPlaces(carats) > CARAT_DP) {
    return {
      ok: false,
      error:
        unit === 'ct'
          ? `${v} ct has more than ${CARAT_DP} decimal places`
          : `${v} g is ${carats} ct, which has more than ${CARAT_DP} decimal places; carats are stored to ${CARAT_DP}`,
    };
  }
  return { ok: true, carats };
}
