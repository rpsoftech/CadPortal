import { describe, expect, it } from 'vitest';
import { MAX_IMPORT_ROWS, parseMatrixCsv, splitLine } from './matrix-csv';

describe('splitLine', () => {
  it('splits commas and honours quotes', () => {
    expect(splitLine('1.10,2HD,"S 1,5",0.007,50000')).toEqual([
      '1.10',
      '2HD',
      'S 1,5',
      '0.007',
      '50000',
    ]);
    expect(splitLine('1.10,2HD,"say ""hi""",0.007,')).toEqual([
      '1.10',
      '2HD',
      'say "hi"',
      '0.007',
      '',
    ]);
  });
  it('detects tab-separated text pasted from a spreadsheet', () => {
    expect(splitLine('1.10\t2HD\t\t0.007\t50000')).toEqual(['1.10', '2HD', '', '0.007', '50000']);
  });
});

describe('parseMatrixCsv, the older five columns', () => {
  it('parses rows, skips a header and keeps values as strings; a per-carat price stays per carat', () => {
    const r = parseMatrixCsv(
      'size_mm,stone_code,sieve,carats_per_stone,rate_per_ct\n1.10,2hd,+0000,0.0070,50000\n1.20,2HD,,0.0095,\n',
    );
    expect(r.errors).toEqual([]);
    expect(r.rows).toEqual([
      {
        size_mm: '1.10',
        stone_code: '2HD',
        sieve: '+0000',
        carats_per_stone: '0.0070',
        rate_per_pc: null,
        rate_per_ct: '50000',
        rate_per_g: null,
        rate_basis: '', // the server reads "only a per-carat price" as per carat
      },
      {
        size_mm: '1.20',
        stone_code: '2HD',
        sieve: '',
        carats_per_stone: '0.0095',
        rate_per_pc: null,
        rate_per_ct: null,
        rate_per_g: null,
        rate_basis: '',
      },
    ]);
  });

  it('works without a header, with CRLF, a BOM and blank lines', () => {
    const r = parseMatrixCsv('﻿1.10,2HD,,0.007,\r\n\r\n1.20,2HD,,0.0095,\r\n');
    expect(r.errors).toEqual([]);
    expect(r.rows).toHaveLength(2);
  });

  it('accepts pasted spreadsheet data (tabs)', () => {
    const r = parseMatrixCsv('1.10\t1HD\t\t0.0050\t45000');
    expect(r.errors).toEqual([]);
    expect(r.rows[0].rate_per_ct).toBe('45000');
  });

  it.each([
    ['1.10,2HD,,0.007', 'expected 5 columns'],
    ['abc,2HD,,0.007,', 'size_mm "abc" is not a number'],
    ['0,2HD,,0.007,', 'size_mm must be greater than 0'],
    ['1.105,2HD,,0.007,', 'size_mm "1.105" has more than 2 decimal places'],
    ['1.10,,,0.007,', 'stone_code'],
    ['1.10,2 HD,,0.007,', 'stone_code'],
    ['1.10,2HD,,-0.1,', 'carats_per_stone "-0.1" is not a number'],
    ['1.10,2HD,,0.000000001,', 'carats_per_stone "0.000000001" has more than 8 decimal places'],
    ['1.10,2HD,,,', 'carats_per_stone or grams_per_stone is required'],
    ['1.10,2HD,,0.007,12.345', 'rate_per_ct "12.345" has more than 2 decimal places'],
    ['1.10,2HD,,0.007,-5', 'rate_per_ct'],
    ['1.10,2HD,12345678901234567890123,0.007,', 'sieve is too long'],
  ])('rejects %j with a message containing %j', (line, fragment) => {
    const r = parseMatrixCsv(line);
    expect(r.rows).toEqual([]);
    expect(r.errors).toHaveLength(1);
    expect(r.errors[0]).toContain('Line 1');
    expect(r.errors[0]).toContain(fragment);
  });

  it('never defaults a missing weight to zero', () => {
    expect(parseMatrixCsv('1.10,2HD,,,50000').rows).toEqual([]);
  });

  it('reports every bad line with its number and keeps the good ones', () => {
    const r = parseMatrixCsv('1.10,2HD,,0.007,\nbad line\n1.20,2HD,,0.0095,\n1.30,2HD,,x,');
    expect(r.rows.map((x) => x.size_mm)).toEqual(['1.10', '1.20']);
    expect(r.errors.map((e) => e.slice(0, 7))).toEqual(['Line 2:', 'Line 4:']);
  });

  it('flags duplicates, treating 1.1 and 1.10 as the same size', () => {
    const r = parseMatrixCsv('1.10,2HD,,0.007,\n1.1,2hd,,0.008,');
    expect(r.rows).toHaveLength(1);
    expect(r.errors[0]).toContain('duplicate of line 1');
  });

  it('enforces the row limit', () => {
    const lines = Array.from({ length: MAX_IMPORT_ROWS + 1 }, (_, i) => `${i + 1}.00,2HD,,0.001,`);
    const r = parseMatrixCsv(lines.join('\n'));
    expect(r.rows).toEqual([]);
    expect(r.errors[0]).toContain('Too many rows');
  });

  it('an empty input gives no rows and no errors', () => {
    expect(parseMatrixCsv('  \n\n')).toEqual({ rows: [], errors: [] });
  });
});

describe('parseMatrixCsv with a header: grams and prices per piece or per carat', () => {
  it('reads grams exactly and prices per piece, in any column order', () => {
    const r = parseMatrixCsv(
      'stone_code,size_mm,grams_per_stone,rate_per_pc,rate_basis\n2HD,1.10,0.0014,100,pc\n2HD,1.20,0.0019,,',
    );
    expect(r.errors).toEqual([]);
    expect(r.rows[0]).toEqual({
      size_mm: '1.10',
      stone_code: '2HD',
      sieve: '',
      carats_per_stone: '0.007', // 0.0014 g is exactly 0.007 ct
      rate_per_pc: '100',
      rate_per_ct: null,
      rate_per_g: null,
      rate_basis: 'pc',
    });
    expect(r.rows[1].carats_per_stone).toBe('0.0095');
    expect(r.rows[1].rate_basis).toBe('');
  });

  it('accepts friendly header names', () => {
    const r = parseMatrixCsv(
      'Size,Code,Grams,Price per piece,Price per carat\n1.10,2HD,0.0014,100,',
    );
    expect(r.errors).toEqual([]);
    expect(r.rows[0].rate_per_pc).toBe('100');
  });

  it('reads the full eight-column export as it is written, carats winning over grams', () => {
    const r = parseMatrixCsv(
      'size_mm,stone_code,sieve,carats_per_stone,grams_per_stone,rate_basis,rate_per_pc,rate_per_ct\n' +
        '1.10,2HD,+0000,0.0070,0.00140,ct,,50000.00\n' +
        '1.20,2HD,,0.0095,0.00190,pc,150.00,\n',
    );
    expect(r.errors).toEqual([]);
    expect(r.rows[0]).toMatchObject({
      carats_per_stone: '0.0070',
      rate_basis: 'ct',
      rate_per_ct: '50000.00',
      sieve: '+0000',
    });
    expect(r.rows[1]).toMatchObject({ rate_basis: 'pc', rate_per_pc: '150.00', rate_per_ct: null });
    // The same file without a header reads the same by position.
    const noHeader = parseMatrixCsv('1.10,2HD,+0000,0.0070,0.00140,ct,,50000.00');
    expect(noHeader.errors).toEqual([]);
    expect(noHeader.rows[0].rate_basis).toBe('ct');
  });

  it('reads per-gram prices from the nine-column export, and the older eight without one', () => {
    const r = parseMatrixCsv(
      'size_mm,stone_code,sieve,carats_per_stone,grams_per_stone,rate_basis,rate_per_pc,rate_per_ct,rate_per_g\n' +
        '1.10,2HD,,0.0070,0.00140,g,,,5000.50\n' +
        '1.20,2HD,,0.0095,0.00190,pc,150.00,,\n',
    );
    expect(r.errors).toEqual([]);
    expect(r.rows[0]).toMatchObject({ rate_basis: 'g', rate_per_g: '5000.50', rate_per_pc: null });
    expect(r.rows[1]).toMatchObject({ rate_basis: 'pc', rate_per_pc: '150.00', rate_per_g: null });
    // Without a header: nine columns by position, and the eight written before per-gram prices.
    const nine = parseMatrixCsv('1.10,2HD,,0.0070,0.00140,g,,,5000.50');
    expect(nine.errors).toEqual([]);
    expect(nine.rows[0]).toMatchObject({ rate_basis: 'g', rate_per_g: '5000.50' });
    const eight = parseMatrixCsv('1.10,2HD,+0000,0.0070,0.00140,ct,,50000.00');
    expect(eight.rows[0]).toMatchObject({
      rate_basis: 'ct',
      rate_per_ct: '50000.00',
      rate_per_g: null,
    });
    // Old header files have no per-gram column at all.
    const old = parseMatrixCsv(
      'size_mm,stone_code,grams_per_stone,rate_per_pc,rate_per_ct,rate_basis\n1.10,2HD,0.0014,,50000,ct\n',
    );
    expect(old.errors).toEqual([]);
    expect(old.rows[0].rate_per_g).toBeNull();
  });

  it('checks the per-gram price like the others, and a g basis needs it', () => {
    const head = 'size_mm,stone_code,grams_per_stone,rate_per_g,rate_basis\n';
    expect(parseMatrixCsv(head + '1.10,2HD,0.0014,12.345,g').errors[0]).toContain(
      'rate_per_g "12.345" has more than 2 decimal places',
    );
    expect(parseMatrixCsv(head + '1.10,2HD,0.0014,-5,g').errors[0]).toContain('rate_per_g');
    expect(parseMatrixCsv(head + '1.10,2HD,0.0014,,g').errors[0]).toContain(
      'rate_basis g needs a rate_per_g',
    );
    expect(parseMatrixCsv(head + '1.10,2HD,0.0014,5000,gram').errors[0]).toContain('rate_basis');
    // Friendly header names.
    const r = parseMatrixCsv('size,code,grams,price_per_gram,basis\n1.10,2hd,0.0014,5000,G\n');
    expect(r.errors).toEqual([]);
    expect(r.rows[0]).toMatchObject({ stone_code: '2HD', rate_per_g: '5000', rate_basis: 'g' });
  });

  it('refuses a weight that would have to be rounded, and says what it came to', () => {
    const r = parseMatrixCsv('size_mm,stone_code,grams_per_stone\n1.10,2HD,0.000000001');
    expect(r.rows).toEqual([]);
    expect(r.errors[0]).toContain('Line 2');
    expect(r.errors[0]).toContain('0.000000005 ct');
  });

  it.each([
    ['size_mm,stone_code\n1.10,2HD', 'carats_per_stone or grams_per_stone column'],
    ['stone_code,carats_per_stone\n2HD,0.007', 'size_mm column'],
    ['size_mm,carats_per_stone\n1.10,0.007', 'stone_code column'],
  ])('a header without what is needed (%j) is refused', (text, fragment) => {
    const r = parseMatrixCsv(text);
    expect(r.errors[0]).toContain(fragment);
  });

  it.each([
    [
      '1.10,2HD,,0.007,,gram,,',
      'rate_basis "gram" must be pc (per piece), ct (per carat) or g (per gram)',
    ],
    ['1.10,2HD,,0.007,,pc,1.005,', 'rate_per_pc "1.005" has more than 2 decimal places'],
    ['1.10,2HD,,0.007,,pc,-1,', 'rate_per_pc'],
  ])('rejects %j', (line, fragment) => {
    const r = parseMatrixCsv(line);
    expect(r.rows).toEqual([]);
    expect(r.errors[0]).toContain(fragment);
  });
});
