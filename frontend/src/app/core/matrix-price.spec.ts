import { describe, expect, it } from 'vitest';
import { otherPrices, priceFor, pricesForSave } from './matrix-price';

const row = { rate_per_pc: '100.00', rate_per_ct: null, rate_per_g: '5000.00' };

describe('matrix price helpers', () => {
  it('reads the price of each basis', () => {
    expect(priceFor(row, 'pc')).toBe('100.00');
    expect(priceFor(row, 'ct')).toBeNull();
    expect(priceFor(row, 'g')).toBe('5000.00');
    expect(priceFor(null, 'g')).toBeNull();
  });

  it('lists the prices kept on the other bases', () => {
    expect(otherPrices(row, 'pc')).toEqual([{ basis: 'g', price: '5000.00' }]);
    expect(otherPrices(row, 'g')).toEqual([{ basis: 'pc', price: '100.00' }]);
    expect(otherPrices(row, 'ct').map((x) => x.basis)).toEqual(['pc', 'g']);
  });

  it('puts the typed price on the chosen basis and keeps the others', () => {
    expect(pricesForSave(row, 'g', '6000')).toEqual({
      rate_per_pc: '100.00',
      rate_per_ct: null,
      rate_per_g: '6000',
    });
    expect(pricesForSave(row, 'ct', '45000')).toEqual({
      rate_per_pc: '100.00',
      rate_per_ct: '45000',
      rate_per_g: '5000.00',
    });
    expect(pricesForSave(null, 'g', null)).toEqual({
      rate_per_pc: null,
      rate_per_ct: null,
      rate_per_g: null,
    });
  });
});
