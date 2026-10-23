import { describe, expect, it } from 'vitest';
import { signed } from './costing-sheet-detail.component';

describe('signed', () => {
  it.each([
    ['12', '+12.00'],
    ['-3.5', '-3.50'],
    ['0', '0.00'],
    ['0.00', '0.00'],
    [null, '–'],
  ])('%s is shown as %s', (v, want) => expect(signed(v)).toBe(want));
});
