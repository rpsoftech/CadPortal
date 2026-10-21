import { Pipe, PipeTransform } from '@angular/core';

/**
 * Pads a decimal string to a fixed number of decimal places without converting it to a
 * JavaScript number (the API trims trailing zeros: "3.05" -> "3.050"). Values with more
 * places than `dp` are returned unchanged rather than rounded here; rounding is the
 * server's job. Null or empty gives an en dash.
 */
export function fixed(value: string | null | undefined, dp: number): string {
  if (value === null || value === undefined || value === '') return '–';
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(value.trim());
  if (!m) return value;
  const frac = m[3] ?? '';
  if (frac.length > dp) return value;
  return `${m[1]}${m[2]}${dp > 0 ? '.' : ''}${frac.padEnd(dp, '0')}`;
}

@Pipe({ name: 'fixed' })
export class FixedPipe implements PipeTransform {
  transform(value: string | null | undefined, dp = 3): string {
    return fixed(value, dp);
  }
}

/**
 * Stone weight in grams for a weight in carats (1 ct = 0.200 g), as an exact decimal string with
 * `dp` places, rounded half up. Done with integers, never floating point, so 0.3470 ct is exactly
 * 0.0694 g. Null, empty or unreadable input gives null.
 */
export function caratsToGrams(carats: string | null | undefined, dp = 4): string | null {
  const m = /^(\d+)(?:\.(\d+))?$/.exec((carats ?? '').trim());
  if (!m) return null;
  const frac = m[2] ?? '';
  // carats = digits / 10^frac.length, so grams = digits x 2 / 10^(frac.length + 1)
  let num = BigInt(m[1] + frac) * 2n;
  const exp = frac.length + 1;
  if (exp <= dp) {
    num *= 10n ** BigInt(dp - exp);
  } else {
    const div = 10n ** BigInt(exp - dp);
    num = (num + div / 2n) / div;
  }
  const s = num.toString().padStart(dp + 1, '0');
  return dp > 0 ? `${s.slice(0, s.length - dp)}.${s.slice(s.length - dp)}` : s;
}

@Pipe({ name: 'stoneGrams' })
export class StoneGramsPipe implements PipeTransform {
  /** Grams for a carat value, or an en dash when there is none. */
  transform(carats: string | null | undefined, dp = 4): string {
    return caratsToGrams(carats, dp) ?? '–';
  }
}
