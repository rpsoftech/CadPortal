import { describe, expect, it } from 'vitest';
import { jobBagsRequest, skippedCodes } from './print-job-bags-dialog.component';

describe('jobBagsRequest', () => {
  it('asks for every variation of the selected designs in one metal and stone code', () => {
    expect(jobBagsRequest([{ id: 4 }, { id: 9 }], '18KR', '2HD', false)).toEqual({
      design_ids: [4, 9],
      metal_code: '18KR',
      stone_code: '2HD',
      skip_incomplete: false,
    });
  });

  it('passes the skip choice on', () => {
    expect(jobBagsRequest([{ id: 1 }], '18KR', '1HD', true).skip_incomplete).toBe(true);
  });
});

describe('skippedCodes', () => {
  it('splits the X-Skipped header', () => {
    expect(skippedCodes('LR0003-V1, LR0003-V2')).toEqual(['LR0003-V1', 'LR0003-V2']);
  });

  it('is empty without the header', () => {
    expect(skippedCodes(null)).toEqual([]);
    expect(skippedCodes('')).toEqual([]);
  });
});
