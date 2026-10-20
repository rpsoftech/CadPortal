import { splitLine } from './matrix-csv';
import { DesignImportRow } from './models';

export const MAX_DESIGN_IMPORT_ROWS = 1000;

export const DESIGN_COLUMNS = [
  'code',
  'name',
  'category',
  'tags',
  'volume_mm3',
  'ref_weight_g',
  'ref_metal',
] as const;

export interface DesignCsvResult {
  rows: DesignImportRow[];
  errors: string[];
}

/**
 * Parses pasted or uploaded design data into rows for POST /designs/import. Columns, in order:
 * code, name, category, tags (separated by ';'), volume_mm3, ref_weight_g, ref_metal. Trailing
 * empty columns may be left out and a header row is skipped. This only checks the shape of the
 * file; the server checks the values (categories, tags, metals, numbers, existing codes) and
 * reports every problem with its line number.
 */
export function parseDesignCsv(text: string): DesignCsvResult {
  const rows: DesignImportRow[] = [];
  const errors: string[] = [];
  const lines = text.replace(/^﻿/, '').split(/\r?\n/);
  let first = true;

  for (let i = 0; i < lines.length; i++) {
    if (lines[i].trim() === '') continue;
    const line = i + 1;
    const cells = splitLine(lines[i]);
    if (first) {
      first = false;
      if (cells[0].toLowerCase() === 'code') continue; // header
    }
    if (cells.length > DESIGN_COLUMNS.length) {
      errors.push(
        `Line ${line}: expected at most ${DESIGN_COLUMNS.length} columns (${DESIGN_COLUMNS.join(', ')}), found ${cells.length}`,
      );
      continue;
    }
    const [code = '', name = '', category = '', tags = '', vol = '', ref = '', metal = ''] = cells;
    rows.push({
      line,
      code,
      name,
      category,
      tags,
      volume_mm3: vol,
      ref_weight_g: ref,
      ref_metal: metal.toUpperCase(),
    });
  }
  if (rows.length > MAX_DESIGN_IMPORT_ROWS) {
    errors.push(`Too many rows: ${rows.length} (limit ${MAX_DESIGN_IMPORT_ROWS})`);
    return { rows: [], errors };
  }
  return { rows, errors };
}
