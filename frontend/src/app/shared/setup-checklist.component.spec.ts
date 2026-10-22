import { describe, expect, it } from 'vitest';
import { SetupItem } from '../core/models';
import { summarize, tabIndex } from './setup-checklist.component';

const item = (severity: SetupItem['severity']): SetupItem => ({
  key: severity,
  severity,
  title: 't',
  detail: 'd',
  tab: 'alloys',
});

describe('tabIndex', () => {
  it.each([
    ['alloys', 0],
    ['matrix', 1],
    ['categories', 2],
    ['nonsense', 0],
    [null, 0],
    [undefined, 0],
  ])('%s opens tab %i', (tab, want) => expect(tabIndex(tab)).toBe(want));
});

describe('summarize', () => {
  it('counts items by severity', () => {
    expect(summarize([item('required'), item('required'), item('recommended')])).toEqual({
      required: 2,
      recommended: 1,
    });
    expect(summarize([])).toEqual({ required: 0, recommended: 0 });
  });
});

describe('summarize with an item fixed outside the app', () => {
  it('counts a backup warning that has no master data tab', () => {
    const backup: SetupItem = {
      key: 'backup',
      severity: 'required',
      title: 'Backups have stopped',
      detail: 'The last good backup finished 3 days ago.',
      tab: '',
    };
    expect(summarize([backup])).toEqual({ required: 1, recommended: 0 });
  });
});
