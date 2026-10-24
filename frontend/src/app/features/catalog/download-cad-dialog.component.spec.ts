import { describe, expect, it } from 'vitest';
import { kindLabel, missingNote, zipSummary } from './download-cad-dialog.component';

describe('kindLabel', () => {
  it('writes the file types the way people do', () => {
    expect(kindLabel('stl')).toBe('STL');
    expect(kindLabel('3dm')).toBe('3dm');
  });
});

describe('zipSummary', () => {
  it('counts files, size and variations', () => {
    expect(zipSummary({ files: 14, bytes: 40 * 1024 * 1024, variations: 9 }, 'stl')).toBe(
      '14 STL files, 40.0 MB, from 9 variations.',
    );
  });

  it('uses the singular', () => {
    expect(zipSummary({ files: 1, bytes: 2048, variations: 1 }, '3dm')).toBe('1 3dm file, 2 KB, from 1 variation.');
  });

  it('says so when there is nothing to download', () => {
    expect(zipSummary({ files: 0, bytes: 0, variations: 3 }, 'stl')).toBe('None of the 3 variations has a STL file.');
  });
});

describe('missingNote', () => {
  it('is empty when nothing is missing', () => {
    expect(missingNote(0, 'stl')).toBe('');
  });

  it('says what happens to the files that are not there', () => {
    expect(missingNote(1, 'stl')).toBe('1 variation has no STL file. It is listed as missing in MANIFEST.csv inside the ZIP.');
    expect(missingNote(3, '3dm')).toBe('3 variations have no 3dm file. They are listed as missing in MANIFEST.csv inside the ZIP.');
  });
});
