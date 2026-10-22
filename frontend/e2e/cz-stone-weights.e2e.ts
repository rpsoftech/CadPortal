import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { caratsToGrams } from '../src/app/core/format';
import { parseMatrixCsv } from '../src/app/core/matrix-csv';

// docs/imports/cz-stone-weights.csv is the supplier list "grams per 1000 pieces" (CZ stones: STAR, MHD, HD),
// converted to grams per stone. It must always load through the real importer without a single refusal.
test.describe('docs/imports/cz-stone-weights.csv', () => {
  const text = readFileSync('../docs/imports/cz-stone-weights.csv', 'utf8');
  const r = parseMatrixCsv(text);

  test('parses without errors, one row per size and stone code that exists in the list', () => {
    expect(r.errors).toEqual([]);
    expect(r.rows).toHaveLength(35);
    expect(new Set(r.rows.map((x) => x.stone_code))).toEqual(new Set(['STAR', 'MHD', 'HD']));
  });

  test('turns grams per 1000 into exact carats per stone (1.81 g per 1000 = 0.00905 ct)', () => {
    const star1mm = r.rows.find((x) => x.stone_code === 'STAR' && x.size_mm === '1.00');
    expect(star1mm?.carats_per_stone).toBe('0.00905');
    expect(caratsToGrams(star1mm!.carats_per_stone, 5)).toBe('0.00181');
    const hd2 = r.rows.find((x) => x.stone_code === 'HD' && x.size_mm === '2.00');
    expect(hd2?.carats_per_stone).toBe('0.1087'); // 21.74 g per 1000
  });

  test('leaves out sizes a stone does not come in, instead of recording them as zero', () => {
    for (const size of ['1.15', '1.25', '1.35']) {
      expect(r.rows.some((x) => x.stone_code === 'STAR' && x.size_mm === size)).toBe(false);
    }
    expect(r.rows.some((x) => x.stone_code === 'MHD' && x.size_mm === '1.70')).toBe(false);
    expect(r.rows.every((x) => Number(x.carats_per_stone) > 0)).toBe(true);
  });

  test('keeps the supplier size name in the sieve column and leaves prices empty', () => {
    const row = r.rows.find((x) => x.stone_code === 'HD' && x.size_mm === '1.10');
    expect(row?.sieve).toBe('1/10');
    expect(r.rows.every((x) => x.rate_per_pc === null && x.rate_per_ct === null)).toBe(true);
  });
});
