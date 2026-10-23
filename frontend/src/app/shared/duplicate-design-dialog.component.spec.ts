import { describe, expect, it } from 'vitest';
import { copyName } from './duplicate-design-dialog.component';

describe('copyName', () => {
  it.each([
    ['Leaf ring', 'Leaf ring (copy)'],
    ['  Leaf ring  ', 'Leaf ring (copy)'],
    ['', ''], // the server then names the copy
  ])('%j becomes %j', (name, want) => expect(copyName(name)).toBe(want));
});
