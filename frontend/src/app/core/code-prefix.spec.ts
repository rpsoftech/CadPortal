import { describe, expect, it } from 'vitest';
import { normalizePrefix } from './code-prefix';

describe('normalizePrefix', () => {
  it.each([
    ['lr', 'LR'],
    [' ps ', 'PS'],
    ['ABCDEF', 'ABCDEF'],
    ['', ''],
    ['   ', ''],
  ])('%j becomes %j', (raw, want) => expect(normalizePrefix(raw)).toBe(want));

  it.each(['L1', 'TOOLONG', 'L R', 'L-R', '1'])('%j is not a prefix', (raw) =>
    expect(normalizePrefix(raw)).toBeNull(),
  );
});
