import { describe, expect, it } from 'vitest';
import { EMPTY_FILTERS, filtersToParams, paramsToFilters } from './catalog-url';

describe('catalog URL', () => {
  it('a clean catalog has a clean URL', () => {
    expect(filtersToParams(EMPTY_FILTERS)).toEqual({});
  });

  it('round-trips every filter, the sort and the page', () => {
    const value = {
      q: 'LR',
      categoryId: 3,
      metalCode: '22KY',
      stoneCode: '2HD',
      minNetWt: '3',
      maxNetWt: '4',
      minGrossWt: '3.1',
      maxGrossWt: '4.2',
      minStonePct: '5',
      maxStonePct: '6.5',
      maxStoneCts: '0.4',
      sort: 'stone_pct_desc' as const,
      tagIds: [4, 9],
    };
    const params = filtersToParams(value, 2, 48);
    expect(params).toMatchObject({ metal: '22KY', stone: '2HD', pct_min: '5', pct_max: '6.5', tags: '4,9' });
    expect(params['page']).toBe('3');
    expect(params['size']).toBe('48');
    expect(paramsToFilters(params)).toEqual({ value, pageIndex: 2, pageSize: 48 });
  });

  it('ignores garbage in the address instead of trusting it', () => {
    const r = paramsToFilters({ category: 'x', sort: 'drop table', page: '-4', size: '7', tags: '1,a,2' });
    expect(r.value.categoryId).toBeNull();
    expect(r.value.sort).toBe('code');
    expect(r.value.tagIds).toEqual([1, 2]);
    expect(r.pageIndex).toBe(0);
    expect(r.pageSize).toBe(24);
  });
});
