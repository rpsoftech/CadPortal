import { describe, expect, it } from 'vitest';
import { BackupRun } from '../../core/models';
import { ago, filesText, lastGood, runLabel, runState } from './backups.component';

const NOW = new Date('2026-10-22T12:00:00Z');

const run = (over: Partial<BackupRun>): BackupRun => ({
  id: 1,
  started_at: '2026-10-22T02:00:00Z',
  finished_at: '2026-10-22T02:01:00Z',
  ok: true,
  db_bytes: 2048,
  files_copied: 3,
  files_bytes: 1_572_864,
  files_total: 120,
  files_skipped: false,
  error: '',
  ...over,
});

describe('runState', () => {
  it.each([
    ['finished and ok', {}, 'ok'],
    ['finished with a failure', { ok: false }, 'failed'],
    ['started a moment ago and not finished', { finished_at: null, ok: null, started_at: '2026-10-22T11:50:00Z' }, 'running'],
    ['never finished and long ago (killed)', { finished_at: null, ok: null, started_at: '2026-10-22T02:00:00Z' }, 'interrupted'],
  ] as const)('%s', (_name, over, want) => {
    expect(runState(run(over as Partial<BackupRun>), NOW)).toBe(want);
  });

  it('has a word for every state', () => {
    expect(['running', 'ok', 'failed', 'interrupted'].map((s) => runLabel(s as never))).toEqual([
      'Running',
      'Succeeded',
      'Failed',
      'Interrupted',
    ]);
  });
});

describe('filesText', () => {
  it('says what was copied', () => {
    expect(filesText(run({}))).toBe('3 new of 120 (1.5 MB)');
  });
  it('says when there is no file storage', () => {
    expect(filesText(run({ files_skipped: true }))).toBe('No file storage');
  });
  it('is empty while the run is going', () => {
    expect(filesText(run({ finished_at: null, ok: null }))).toBe('');
  });
});

describe('lastGood', () => {
  it('skips failed and unfinished runs', () => {
    const runs = [
      run({ id: 4, finished_at: null, ok: null }),
      run({ id: 3, ok: false }),
      run({ id: 2 }),
      run({ id: 1 }),
    ];
    expect(lastGood(runs)?.id).toBe(2);
  });
  it('is undefined when nothing worked', () => {
    expect(lastGood([run({ ok: false })])).toBeUndefined();
    expect(lastGood([])).toBeUndefined();
  });
});

describe('ago', () => {
  it.each([
    ['2026-10-22T11:59:30Z', 'just now'],
    ['2026-10-22T11:30:00Z', '30 minutes ago'],
    ['2026-10-22T02:00:00Z', '10 hours ago'],
    ['2026-10-19T12:00:00Z', '3 days ago'],
    ['2026-10-23T12:00:00Z', 'just now'], // a clock a little ahead is not "in the future"
  ])('%s is %s', (iso, want) => expect(ago(iso, NOW)).toBe(want));
});
