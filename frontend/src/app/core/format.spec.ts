import { describe, expect, it } from 'vitest';
import { caratsToGrams, fixed } from './format';

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
