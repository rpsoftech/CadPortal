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

describe('parseMatrixCsv', () => {
  it('parses rows, skips a header and keeps values as strings', () => {
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
        rate_per_ct: '50000',
      },
      {
        size_mm: '1.20',
        stone_code: '2HD',
        sieve: '',
        carats_per_stone: '0.0095',
        rate_per_ct: null,
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
    ['1.10,2HD,,0.00701,', 'carats_per_stone "0.00701" has more than 4 decimal places'],
    ['1.10,2HD,,,', 'carats_per_stone'],
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

  it('never defaults a missing carat value to zero', () => {
    const r = parseMatrixCsv('1.10,2HD,,,50000');
    expect(r.rows).toEqual([]);
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
