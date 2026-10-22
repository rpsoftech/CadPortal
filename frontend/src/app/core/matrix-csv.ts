import { MatrixRowInput, RateBasis } from './models';
import { CARAT_DP, decimalPlaces, weightToCarats } from './stone-units';

export const MAX_IMPORT_ROWS = 5000;

export interface CsvParseResult {
  rows: MatrixRowInput[];
  errors: string[];
}

/** The columns of the stone matrix file, in the order the export writes them. */
export const MATRIX_COLUMNS = [
  'size_mm',
  'stone_code',
  'sieve',
  'carats_per_stone',
  'grams_per_stone',
  'rate_basis',
  'rate_per_pc',
  'rate_per_ct',
] as const;
type Column = (typeof MATRIX_COLUMNS)[number];

/** The names a header may use for each column (lower case, spaces and dashes as underscores). */
const ALIASES: Record<string, Column> = {
  size_mm: 'size_mm',
  size: 'size_mm',
  stone_code: 'stone_code',
  code: 'stone_code',
  sieve: 'sieve',
  carats_per_stone: 'carats_per_stone',
  carats: 'carats_per_stone',
  grams_per_stone: 'grams_per_stone',
  grams: 'grams_per_stone',
  rate_basis: 'rate_basis',
  price_basis: 'rate_basis',
  basis: 'rate_basis',
  rate_per_pc: 'rate_per_pc',
  price_per_pc: 'rate_per_pc',
  price_per_piece: 'rate_per_pc',
  rate_per_ct: 'rate_per_ct',
  price_per_ct: 'rate_per_ct',
  price_per_carat: 'rate_per_ct',
};

/** Files without a header: the older five columns, or the full eight. */
const LEGACY: Column[] = ['size_mm', 'stone_code', 'sieve', 'carats_per_stone', 'rate_per_ct'];

/** Splits one line, honouring double quotes. Tab-separated text (pasted from Excel) is detected. */
export function splitLine(line: string): string[] {
  const sep = line.includes('\t') ? '\t' : ',';
  const out: string[] = [];
  let cur = '';
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quoted) {
      if (ch === '"' && line[i + 1] === '"') {
        cur += '"';
        i++;
      } else if (ch === '"') {
        quoted = false;
      } else {
        cur += ch;
      }
    } else if (ch === '"') {
      quoted = true;
    } else if (ch === sep) {
      out.push(cur.trim());
      cur = '';
    } else {
      cur += ch;
    }
  }
  out.push(cur.trim());
  return out;
}

function decimalError(name: string, v: string, maxDp: number, allowZero: boolean): string | null {
  if (!/^\d+(\.\d+)?$/.test(v)) return `${name} "${v}" is not a number`;
  if (decimalPlaces(v) > maxDp) return `${name} "${v}" has more than ${maxDp} decimal places`;
  if (!allowZero && Number(v) === 0) return `${name} must be greater than 0`;
  return null;
}

/** A header row is recognised by its names; null when the line is data. */
function headerColumns(cells: string[]): Column[] | null {
  const names = cells.map((c) => c.toLowerCase().replace(/[\s-]+/g, '_'));
  const known = names.filter((n) => n in ALIASES).length;
  if (known === 0 || known < Math.min(2, names.length)) return null;
  return names.map((n) => ALIASES[n]);
}

/**
 * Parses pasted or uploaded stone matrix data. With a header row, columns may come in any order and
 * under the names above; the weight per stone is given as carats_per_stone or grams_per_stone, and the
 * price as rate_per_pc and/or rate_per_ct with rate_basis (pc or ct) saying which one costing uses.
 * Without a header the older five columns (size, code, sieve, carats, rate per carat) or the full eight
 * are read by position. Values stay strings so no precision is lost; limits mirror the server's (size
 * 2 dp, carats 4 dp, prices 2 dp). A weight that would need rounding is refused. Every problem is
 * reported with its line number; nothing is guessed or defaulted to zero.
 */
export function parseMatrixCsv(text: string): CsvParseResult {
  const rows: MatrixRowInput[] = [];
  const errors: string[] = [];
  const seen = new Map<string, number>();
  const lines = text.replace(/^﻿/, '').split(/\r?\n/);
  let columns: Column[] | null = null;
  let first = true;

  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    if (raw.trim() === '') continue;
    const n = i + 1;
    const cells = splitLine(raw);

    if (first) {
      first = false;
      const head = headerColumns(cells);
      if (head) {
        columns = head;
        for (const need of ['size_mm', 'stone_code'] as Column[]) {
          if (!head.includes(need)) errors.push(`Line ${n}: the header needs a ${need} column`);
        }
        if (!head.includes('carats_per_stone') && !head.includes('grams_per_stone')) {
          errors.push(`Line ${n}: the header needs a carats_per_stone or grams_per_stone column`);
        }
        continue;
      }
    }
    const cols = columns ?? (cells.length === MATRIX_COLUMNS.length ? [...MATRIX_COLUMNS] : LEGACY);
    if (cells.length !== cols.length) {
      errors.push(
        `Line ${n}: expected ${cols.length} columns (${cols.join(', ')}), found ${cells.length}`,
      );
      continue;
    }
    const v: Partial<Record<Column, string>> = {};
    cols.forEach((c, k) => (v[c] = cells[k]));
    const get = (c: Column) => v[c] ?? '';

    const size = get('size_mm');
    const codeRaw = get('stone_code');
    const code = codeRaw.toUpperCase();
    const problems: (string | null)[] = [
      decimalError('size_mm', size, 2, false),
      /^[A-Z0-9]{1,12}$/.test(code)
        ? null
        : `stone_code "${codeRaw}" must be 1 to 12 letters or digits`,
      get('sieve').length > 20 ? 'sieve is too long (20 characters max)' : null,
    ];

    // Weight per stone: carats win when both are given; grams are converted exactly.
    let carats = '';
    if (get('carats_per_stone') !== '') {
      problems.push(decimalError('carats_per_stone', get('carats_per_stone'), CARAT_DP, true));
      carats = get('carats_per_stone');
    } else if (get('grams_per_stone') !== '') {
      const w = weightToCarats(get('grams_per_stone'), 'g');
      problems.push(w.ok ? null : `grams_per_stone ${w.error}`);
      carats = w.ok ? w.carats : '';
    } else {
      problems.push('carats_per_stone or grams_per_stone is required');
    }

    const basis = get('rate_basis').toLowerCase();
    if (basis !== '' && basis !== 'pc' && basis !== 'ct') {
      problems.push(`rate_basis "${get('rate_basis')}" must be pc (per piece) or ct (per carat)`);
    }
    const pc = get('rate_per_pc');
    const ct = get('rate_per_ct');
    if (pc !== '') problems.push(decimalError('rate_per_pc', pc, 2, true));
    if (ct !== '') problems.push(decimalError('rate_per_ct', ct, 2, true));

    const bad = problems.filter((p): p is string => p !== null);
    if (bad.length) {
      errors.push(`Line ${n}: ${bad.join('; ')}`);
      continue;
    }
    const key = `${Number(size).toFixed(2)}|${code}`;
    if (seen.has(key)) {
      errors.push(`Line ${n}: duplicate of line ${seen.get(key)} (${size} mm, ${code})`);
      continue;
    }
    seen.set(key, n);
    rows.push({
      size_mm: size,
      stone_code: code,
      sieve: get('sieve'),
      carats_per_stone: carats,
      rate_per_pc: pc === '' ? null : pc,
      rate_per_ct: ct === '' ? null : ct,
      rate_basis: basis as RateBasis | '',
    });
  }
  if (rows.length > MAX_IMPORT_ROWS) {
    errors.push(`Too many rows: ${rows.length} (limit ${MAX_IMPORT_ROWS})`);
    return { rows: [], errors };
  }
  return { rows, errors };
}
