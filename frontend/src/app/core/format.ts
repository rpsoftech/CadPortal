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
