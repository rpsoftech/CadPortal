import { FormControl } from '@angular/forms';
import { describe, expect, it } from 'vitest';
import { positiveDecimal, positiveInt } from './validators';

const run = (v: unknown, fn: (c: FormControl) => unknown) => fn(new FormControl(v));

describe('positiveDecimal', () => {
  const v = positiveDecimal(3);
  it.each(['200', '3.55', '0.001', ' 12.5 '])('accepts %s', (s) => {
    expect(run(s, v)).toBeNull();
  });
  it('accepts an empty value (required is a separate validator)', () => {
    expect(run('', v)).toBeNull();
  });
  it.each(['0', '0.000', '-1', '1.2345', 'abc', '1e3', '1,5'])('rejects %s', (s) => {
    expect(run(s, v)).not.toBeNull();
  });
});

describe('positiveInt', () => {
  it.each(['1', '24', '007'])('accepts %s', (s) => expect(run(s, positiveInt)).toBeNull());
  it.each(['0', '-3', '1.5', 'x'])('rejects %s', (s) => expect(run(s, positiveInt)).not.toBeNull());
});
