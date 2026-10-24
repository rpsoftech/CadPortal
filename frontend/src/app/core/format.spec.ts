import { describe, expect, it } from 'vitest';
import { caratsToGrams, exactGrams, fixed, formatBytes } from './format';

describe('fixed', () => {
  it.each([
    ['3.05', 3, '3.050'],
    ['3', 3, '3.000'],
    ['0.347', 4, '0.3470'],
    ['0', 4, '0.0000'],
    ['1.1', 2, '1.10'],
    ['3.119', 3, '3.119'],
    ['-2.5', 2, '-2.50'],
    ['12', 0, '12'],
  ])('pads %s to %i places -> %s', (input, dp, want) => {
    expect(fixed(input, dp)).toBe(want);
  });

  it('shows an en dash for missing values', () => {
    expect(fixed(null, 3)).toBe('–');
    expect(fixed(undefined, 3)).toBe('–');
    expect(fixed('', 3)).toBe('–');
  });

  it('never rounds: longer values and non-numbers pass through', () => {
    expect(fixed('3.1195', 3)).toBe('3.1195');
    expect(fixed('abc', 3)).toBe('abc');
  });

  it('keeps precision a float would lose', () => {
    expect(
      fixed('0.1000000000000000055511151231257827', 40).startsWith('0.1000000000000000055'),
    ).toBe(true);
  });
});

describe('caratsToGrams', () => {
  it.each([
    ['0.3470', 4, '0.0694'], // the spec fixture: 37 pcs, 0.3470 ct = 0.069 g
    ['0.1680', 4, '0.0336'],
    ['0.1140', 4, '0.0228'],
    ['0.0650', 4, '0.0130'],
    ['0.3470', 3, '0.069'],
    ['1', 3, '0.200'],
    ['0', 4, '0.0000'],
    ['12345.6789', 4, '2469.1358'], // 2469.13578 rounds half up
    ['0.00005', 4, '0.0000'],
    ['0.0003', 4, '0.0001'], // 0.00006 g
  ])('%s ct at %i dp is %s g', (ct, dp, want) => expect(caratsToGrams(ct, dp)).toBe(want));

  it.each([null, undefined, '', 'abc', '-1', '1,5'])('%j has no grams', (v) =>
    expect(caratsToGrams(v)).toBeNull(),
  );
});

describe('exactGrams', () => {
  it.each([
    ['0.007', 5, '0.0014'.padEnd(7, '0')], // 0.00140: padded to five decimals
    ['0.00905', 5, '0.00181'],
    ['0.12345678', 5, '0.024691356'], // eight decimals of carats need nine of grams
    ['0.0123457', 5, '0.00246914'],
    ['1', 0, '0.2'],
    ['10', 0, '2'],
    ['0', 3, '0.000'],
  ])('%s ct (at least %i decimals) is %s g', (ct, min, want) =>
    expect(exactGrams(ct, min)).toBe(want),
  );

  it.each([null, undefined, '', 'x', '-1'])('%j has no grams', (v) =>
    expect(exactGrams(v)).toBeNull(),
  );
});

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [900, '900 B'],
    [2048, '2 KB'],
    [12.4 * 1024 * 1024, '12.4 MB'],
    [250 * 1024 * 1024, '250.0 MB'],
    [1.25 * 1024 ** 3, '1.3 GB'],
    [-1, '–'],
    [Number.NaN, '–'],
  ])('%d is %s', (n, want) => expect(formatBytes(n)).toBe(want));
});
