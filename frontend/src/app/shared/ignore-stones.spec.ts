import { describe, expect, it } from 'vitest';
import { IGNORE_OVER_DEFAULT_MM, ignoreOverPart } from './ignore-stones';

describe('ignoreOverPart', () => {
  it('sends nothing while the choice is off, whatever the size says', () => {
    expect(ignoreOverPart(false, '2.00')).toEqual({});
    expect(ignoreOverPart(false, null)).toEqual({});
  });

  it('sends the size, trimmed, when the choice is on', () => {
    expect(ignoreOverPart(true, ' 2.50 ')).toEqual({ ignore_stones_over_mm: '2.50' });
    expect(ignoreOverPart(true, IGNORE_OVER_DEFAULT_MM)).toEqual({ ignore_stones_over_mm: '2.00' });
  });

  it('sends nothing for an empty size rather than a blank one', () => {
    expect(ignoreOverPart(true, '')).toEqual({});
    expect(ignoreOverPart(true, null)).toEqual({});
  });
});
