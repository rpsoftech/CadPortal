import { describe, expect, it } from 'vitest';
import { nextDesignCode } from './new-design-dialog.component';

describe('nextDesignCode', () => {
  it.each([
    ['LR', 0, 'LR0001'],
    ['LR', 6, 'LR0007'],
    ['PS', 41, 'PS0042'],
    ['LR', 9999, 'LR10000'], // grows rather than truncating, like the server
  ])('%s after %i is %s', (prefix, last, want) => {
    expect(nextDesignCode(prefix, last)).toBe(want);
  });
});
