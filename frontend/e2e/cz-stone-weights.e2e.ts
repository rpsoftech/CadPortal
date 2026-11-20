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

  test('reads the same weights when pasted without the header line (columns are in the export order)', () => {
    const headerless = parseMatrixCsv(text.split('\n').slice(1).join('\n'));
    expect(headerless.errors).toEqual([]);
    expect(headerless.rows).toEqual(r.rows);
    const star1mm = headerless.rows.find((x) => x.stone_code === 'STAR' && x.size_mm === '1.00');
    expect(caratsToGrams(star1mm!.carats_per_stone, 5)).toBe('0.00181'); // grams, not read as carats
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
    expect(r.rows.every((x) => x.rate_per_pc === null && x.rate_per_ct === null && x.rate_per_g === null)).toBe(true);
  });
});

// docs/imports/cz-stone-weights-star-priced.csv is the same list with the owner's STAR price filled in
// (500 per 1000 pieces = 0.50 per piece, sizes 1.00 to 1.50 mm). It must load without a refusal, carry exactly
// the supplier weights, and price nothing else.
test.describe('docs/imports/cz-stone-weights-star-priced.csv', () => {
  const plain = parseMatrixCsv(readFileSync('../docs/imports/cz-stone-weights.csv', 'utf8'));
  const priced = parseMatrixCsv(readFileSync('../docs/imports/cz-stone-weights-star-priced.csv', 'utf8'));
  const pricedSizes = ['1.00', '1.10', '1.20', '1.30', '1.40', '1.50'];

  test('parses without errors and has the same rows and weights as the supplier list', () => {
    expect(priced.errors).toEqual([]);
    expect(priced.rows).toHaveLength(plain.rows.length);
    const key = (x: { size_mm: string; stone_code: string; carats_per_stone: string; sieve: string | null }) =>
      `${x.stone_code}|${x.size_mm}|${x.carats_per_stone}|${x.sieve}`;
    expect(priced.rows.map(key)).toEqual(plain.rows.map(key));
  });

  test('prices STAR 1.00 to 1.50 mm at 0.50 per piece and nothing else', () => {
    for (const x of priced.rows) {
      const isPriced = x.stone_code === 'STAR' && pricedSizes.includes(x.size_mm);
      expect(x.rate_per_pc, `${x.stone_code} ${x.size_mm}`).toBe(isPriced ? '0.50' : null);
      expect(x.rate_per_ct).toBeNull();
      expect(x.rate_per_g).toBeNull();
    }
    expect(priced.rows.filter((x) => x.rate_per_pc !== null)).toHaveLength(pricedSizes.length);
  });
});

// docs/imports/stone-matrix-default.csv is the owner's own list for the stone codes STAR, 1HD and 2HD: the weight
// of one stone in grams and the price per piece. It must load without a refusal, with or without its header line.
test.describe('docs/imports/stone-matrix-default.csv', () => {
  const text = readFileSync('../docs/imports/stone-matrix-default.csv', 'utf8');
  const r = parseMatrixCsv(text);

  test('parses without errors, 35 rows over STAR, 1HD and 2HD', () => {
    expect(r.errors).toEqual([]);
    expect(r.rows).toHaveLength(35);
    const count = (code: string) => r.rows.filter((x) => x.stone_code === code).length;
    expect([count('STAR'), count('1HD'), count('2HD')]).toEqual([11, 10, 14]);
  });

  test('reads the weight as grams (0.00181 g = 0.00905 ct) and prices every row per piece', () => {
    const star1mm = r.rows.find((x) => x.stone_code === 'STAR' && x.size_mm === '1.00');
    expect(star1mm?.carats_per_stone).toBe('0.00905');
    expect(caratsToGrams(star1mm!.carats_per_stone, 5)).toBe('0.00181');
    expect(r.rows.every((x) => x.rate_basis === 'pc' && x.rate_per_pc !== null)).toBe(true);
  });

  test('reads the same rows when pasted without the header line', () => {
    const headerless = parseMatrixCsv(text.split('\n').slice(1).join('\n'));
    expect(headerless.errors).toEqual([]);
    expect(headerless.rows).toEqual(r.rows);
  });
});
