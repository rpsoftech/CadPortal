import { describe, expect, it } from 'vitest';
import { fixed } from './format';

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
