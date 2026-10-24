import { Params } from '@angular/router';
import { DesignSort } from '../../core/models';

/** The catalog filters as they sit in the form (text as typed, ids as numbers). */
export interface CatalogFormValue {
  q: string | null;
  categoryId: number | null;
  metalCode: string | null;
  stoneCode: string | null;
  minNetWt: string | null;
  maxNetWt: string | null;
  minGrossWt: string | null;
  maxGrossWt: string | null;
  minStonePct: string | null;
  maxStonePct: string | null;
  maxStoneCts: string | null;
  sort: DesignSort;
  tagIds: number[];
}

export const SORTS: readonly DesignSort[] = [
  'code',
  'net_asc',
  'net_desc',
  'gross_asc',
  'gross_desc',
  'stone_pct_asc',
  'stone_pct_desc',
];

export const EMPTY_FILTERS: CatalogFormValue = {
  q: '',
  categoryId: null,
  metalCode: null,
  stoneCode: null,
  minNetWt: '',
  maxNetWt: '',
  minGrossWt: '',
  maxGrossWt: '',
  minStonePct: '',
  maxStonePct: '',
  maxStoneCts: '',
  sort: 'code',
  tagIds: [],
};

/** form field -> short query parameter */
const KEYS: [keyof CatalogFormValue, string][] = [
  ['q', 'q'],
  ['categoryId', 'category'],
  ['metalCode', 'metal'],
  ['stoneCode', 'stone'],
  ['minNetWt', 'net_min'],
  ['maxNetWt', 'net_max'],
  ['minGrossWt', 'gross_min'],
  ['maxGrossWt', 'gross_max'],
  ['minStonePct', 'pct_min'],
  ['maxStonePct', 'pct_max'],
  ['maxStoneCts', 'stone_max_g'],
  ['sort', 'sort'],
];

/**
 * The query parameters that describe the current filters: only what is set, so a clean catalog has a
 * clean URL. Page and page size ride along when they are not the defaults.
 */
export function filtersToParams(v: CatalogFormValue, pageIndex = 0, pageSize = 24): Params {
  const out: Params = {};
  for (const [field, key] of KEYS) {
    const val = v[field];
    if (val === null || val === undefined || `${val}`.trim() === '') continue;
    if (field === 'sort' && val === 'code') continue;
    out[key] = `${val}`.trim();
  }
  if (v.tagIds.length) out['tags'] = v.tagIds.join(',');
  if (pageIndex > 0) out['page'] = `${pageIndex + 1}`;
  if (pageSize !== 24) out['size'] = `${pageSize}`;
  return out;
}

/** The inverse of filtersToParams; anything unreadable is ignored rather than trusted. */
export function paramsToFilters(p: Params): {
  value: CatalogFormValue;
  pageIndex: number;
  pageSize: number;
} {
  const get = (k: string): string => (typeof p[k] === 'string' ? (p[k] as string) : '');
  const int = (k: string): number | null => {
    const s = get(k);
    return /^\d+$/.test(s) ? Number(s) : null;
  };
  const sort = get('sort') as DesignSort;
  const size = int('size');
  const page = int('page');
  return {
    value: {
      q: get('q'),
      categoryId: int('category'),
      metalCode: get('metal') || null,
      stoneCode: get('stone') || null,
      minNetWt: get('net_min'),
      maxNetWt: get('net_max'),
      minGrossWt: get('gross_min'),
      maxGrossWt: get('gross_max'),
      minStonePct: get('pct_min'),
      maxStonePct: get('pct_max'),
      maxStoneCts: get('stone_max_g'),
      sort: SORTS.includes(sort) ? sort : 'code',
      tagIds: get('tags')
        .split(',')
        .filter((s) => /^\d+$/.test(s))
        .map(Number),
    },
    pageIndex: page !== null && page > 0 ? page - 1 : 0,
    pageSize: size !== null && [12, 24, 48, 96].includes(size) ? size : 24,
  };
}
