import { describe, expect, it } from 'vitest';
import { Order } from '../../core/models';
import { orderHasFiles, orderKinds, orderZipNotes, orderZipSummary } from './order-download-dialog.component';

const file = { name: 'a', sha256: 'x', size_bytes: 1 };
const order = (...lines: { stl?: boolean; dm?: boolean }[]) =>
  ({
    lines: lines.map((l) => ({ snapshot: { file_stl: l.stl ? file : null, file_3dm: l.dm ? file : null } })),
  }) as unknown as Order;

describe('orderKinds', () => {
  it('asks the package for both kinds', () => {
    expect(orderKinds('package')).toEqual(['stl', '3dm']);
    expect(orderKinds('stl')).toEqual(['stl']);
    expect(orderKinds('3dm')).toEqual(['3dm']);
  });
});

describe('orderHasFiles', () => {
  it('looks at the files the order recorded', () => {
    const o = order({ stl: true }, {});
    expect(orderHasFiles(o, 'stl')).toBe(true);
    expect(orderHasFiles(o, '3dm')).toBe(false);
    expect(orderHasFiles(o, 'package')).toBe(true);
    expect(orderHasFiles(order({}), 'package')).toBe(false);
  });
});

describe('orderZipSummary', () => {
  it('counts files, size and lines', () => {
    expect(orderZipSummary({ files: 2, bytes: 2048, lines: 3, package: false }, 'stl')).toBe(
      '2 CAD files (STL files), 2 KB, from 3 lines.',
    );
  });

  it('says so when there is nothing to download', () => {
    expect(orderZipSummary({ files: 0, bytes: 0, lines: 1, package: false }, '3dm')).toBe('None of the 1 line has a 3dm file.');
    expect(orderZipSummary({ files: 0, bytes: 0, lines: 2, package: true }, 'package')).toContain('still holds the job bags');
  });
});

describe('orderZipNotes', () => {
  it('is empty when nothing is missing or changed', () => {
    expect(orderZipNotes({ missing: 0, changed: 0 })).toEqual([]);
  });

  it('names missing and changed files', () => {
    const n = orderZipNotes({ missing: 2, changed: 1 });
    expect(n).toHaveLength(2);
    expect(n[0]).toContain('2 requested files are not on the order');
    expect(n[1]).toContain('1 file was replaced or removed');
  });
});
