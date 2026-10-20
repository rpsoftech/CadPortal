import { describe, expect, it } from 'vitest';
import { weightRange } from './catalog.component';

describe('weightRange', () => {
  it('pads to 3 places and shows a range', () => {
    expect(weightRange('3.05', '6.1')).toBe('3.050 – 6.100 g');
  });
  it('collapses to one value when min equals max', () => {
    expect(weightRange('3.05', '3.05')).toBe('3.050 g');
  });
  it('is null when no weight is known', () => {
    expect(weightRange(null, null)).toBeNull();
    expect(weightRange('3.05', null)).toBeNull();
  });
});
