import { describe, expect, it } from 'vitest';
import { decimalPlaces, gramsToCarats, weightToCarats } from './stone-units';

describe('gramsToCarats', () => {
  it.each([
    ['0.0014', '0.007'],
    ['0.2', '1'],
    ['1', '5'],
    ['0.0013', '0.0065'],
    ['0.00001', '0.00005'],
    ['0', '0'],
    ['12.5', '62.5'],
  ])('%s g is %s ct', (g, ct) => expect(gramsToCarats(g)).toBe(ct));

  it.each([null, undefined, '', 'abc', '-1', '1,5', '1e3'])('%j is not a weight', (v) =>
    expect(gramsToCarats(v)).toBeNull(),
  );
});

describe('decimalPlaces', () => {
  it.each([
    ['1', 0],
    ['0.0070', 4],
    ['12.345', 3],
  ])('%s has %i', (v, n) => expect(decimalPlaces(v)).toBe(n));
});

describe('weightToCarats', () => {
  it('converts grams and keeps carats as typed (trailing zeros removed)', () => {
    expect(weightToCarats('0.0014', 'g')).toEqual({ ok: true, carats: '0.007' });
    expect(weightToCarats('0.0070', 'ct')).toEqual({ ok: true, carats: '0.007' });
    expect(weightToCarats('1.0000', 'ct')).toEqual({ ok: true, carats: '1' });
    expect(weightToCarats('0.0095', 'ct')).toEqual({ ok: true, carats: '0.0095' });
    expect(weightToCarats('0.0003', 'g')).toEqual({ ok: true, carats: '0.0015' });
  });

  it('refuses what would need rounding, saying why', () => {
    const g = weightToCarats('0.00035', 'g'); // 0.00175 ct
    expect(g.ok === false && g.error).toContain('0.00175 ct');
    const ct = weightToCarats('0.00701', 'ct');
    expect(ct.ok === false && ct.error).toContain('more than 4 decimal places');
  });

  it.each(['', 'x', '-0.1', '1,5'])('refuses %j', (v) => {
    expect(weightToCarats(v, 'g').ok).toBe(false);
    expect(weightToCarats(v, 'ct').ok).toBe(false);
  });
});
