import { describe, expect, it } from 'vitest';
import { MAX_QUANTITY, parseQuantity } from './order-new.component';

describe('parseQuantity', () => {
  it.each([
    ['1', 1],
    [' 25 ', 25],
    [String(MAX_QUANTITY), MAX_QUANTITY],
  ])('%s is a valid quantity', (raw, want) => expect(parseQuantity(raw)).toBe(want));

  it.each(['', '0', '-1', '1.5', 'abc', '1e3', String(MAX_QUANTITY + 1)])('%s is refused', (raw) =>
    expect(parseQuantity(raw)).toBeNull(),
  );
});
