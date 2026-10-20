import { MatrixRowInput } from './models';

export const MAX_IMPORT_ROWS = 5000;

export interface CsvParseResult {
  rows: MatrixRowInput[];
  errors: string[];
}

const COLUMNS = ['size_mm', 'stone_code', 'sieve', 'carats_per_stone', 'rate_per_ct'];

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
  const frac = v.split('.')[1] ?? '';
  if (frac.length > maxDp) return `${name} "${v}" has more than ${maxDp} decimal places`;
  if (!allowZero && Number(v) === 0) return `${name} must be greater than 0`;
  return null;
}

/**
 * Parses pasted or uploaded matrix data. Columns, in order:
 * size_mm, stone_code, sieve (optional), carats_per_stone, rate_per_ct (optional).
 * A header row is skipped. Values stay strings so no precision is lost, and the
 * limits mirror the server's (size 2 dp, carats 4 dp, rate 2 dp). Every problem is
 * reported with its line number; nothing is guessed or defaulted to zero.
 */
export function parseMatrixCsv(text: string): CsvParseResult {
  const rows: MatrixRowInput[] = [];
  const errors: string[] = [];
  const seen = new Map<string, number>();
  const lines = text.replace(/^﻿/, '').split(/\r?\n/);
  let first = true;

  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    if (raw.trim() === '') continue;
    const n = i + 1;
    const cells = splitLine(raw);

    if (first) {
      first = false;
      const head = cells.map((c) => c.toLowerCase().replace(/[\s-]+/g, '_'));
      if (
        head[0] === 'size_mm' ||
        head[0] === 'size' ||
        (head[1] === 'stone_code' && !/^\d/.test(cells[0]))
      ) {
        if (
          !COLUMNS.every(
            (c, k) => k >= 5 || head[k] === undefined || head[k] === c || head[k] === '',
          )
        ) {
          errors.push(`Line ${n}: header should be ${COLUMNS.join(', ')}`);
        }
        continue;
      }
    }
    if (cells.length !== 5) {
      errors.push(`Line ${n}: expected 5 columns (${COLUMNS.join(', ')}), found ${cells.length}`);
      continue;
    }
    const [size, codeRaw, sieve, carats, rate] = cells;
    const code = codeRaw.toUpperCase();
    const problems = [
      decimalError('size_mm', size, 2, false),
      /^[A-Z0-9]{1,12}$/.test(code)
        ? null
        : `stone_code "${codeRaw}" must be 1 to 12 letters or digits`,
      decimalError('carats_per_stone', carats, 4, true),
      rate === '' ? null : decimalError('rate_per_ct', rate, 2, true),
      sieve.length > 20 ? 'sieve is too long (20 characters max)' : null,
    ].filter((p): p is string => p !== null);
    if (problems.length) {
      errors.push(`Line ${n}: ${problems.join('; ')}`);
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
      sieve,
      carats_per_stone: carats,
      rate_per_ct: rate === '' ? null : rate,
    });
  }
  if (rows.length > MAX_IMPORT_ROWS) {
    errors.push(`Too many rows: ${rows.length} (limit ${MAX_IMPORT_ROWS})`);
    return { rows: [], errors };
  }
  return { rows, errors };
}
