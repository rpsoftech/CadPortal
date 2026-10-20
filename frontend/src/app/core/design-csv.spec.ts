import { describe, expect, it } from 'vitest';
import { MAX_DESIGN_IMPORT_ROWS, parseDesignCsv } from './design-csv';

describe('parseDesignCsv', () => {
  it('skips the header and keeps line numbers', () => {
    const r = parseDesignCsv(
      'code,name,category,tags,volume_mm3,ref_weight_g,ref_metal\n\nLR001,Lotus,Rings,Floral;Bridal,200,,\n',
    );
    expect(r.errors).toEqual([]);
    expect(r.rows).toEqual([
      {
        line: 3,
        code: 'LR001',
        name: 'Lotus',
        category: 'Rings',
        tags: 'Floral;Bridal',
        volume_mm3: '200',
        ref_weight_g: '',
        ref_metal: '',
      },
    ]);
  });

  it('works without a header and with trailing columns left out', () => {
    const r = parseDesignCsv('LR002,Stud,Earrings');
    expect(r.errors).toEqual([]);
    expect(r.rows[0]).toMatchObject({ line: 1, code: 'LR002', category: 'Earrings', tags: '' });
    expect(r.rows[0].volume_mm3).toBe('');
  });

  it('keeps quoted commas and reads tab-separated text pasted from a spreadsheet', () => {
    const r = parseDesignCsv('LR003,"Ring, big",Rings\nLR004\tPlain\tRings\t\t\t3.116\t18ky');
    expect(r.rows[0].name).toBe('Ring, big');
    expect(r.rows[1]).toMatchObject({ code: 'LR004', ref_weight_g: '3.116', ref_metal: '18KY' });
  });

  it('rejects rows with too many columns without dropping the rest', () => {
    const r = parseDesignCsv('a,b,c,d,e,f,g,h\nLR005,x,Rings');
    expect(r.errors).toEqual([expect.stringContaining('Line 1: expected at most 7 columns')]);
    expect(r.rows).toHaveLength(1);
  });

  it('refuses files over the row limit', () => {
    const text = Array.from({ length: MAX_DESIGN_IMPORT_ROWS + 1 }, (_, i) => `X${i},,Rings`).join(
      '\n',
    );
    const r = parseDesignCsv(text);
    expect(r.rows).toEqual([]);
    expect(r.errors[0]).toMatch(/Too many rows/);
  });
});
