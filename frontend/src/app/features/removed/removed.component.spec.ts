import { describe, expect, it } from 'vitest';
import { daysLeft, reasonText } from './removed.component';

describe('daysLeft', () => {
  const now = new Date('2026-10-21T12:00:00Z');
  it.each([
    ['2026-11-20T12:00:00Z', 30],
    ['2026-10-22T11:00:00Z', 1], // part of a day counts as a day
    ['2026-10-21T12:00:01Z', 1],
    ['2026-10-21T12:00:00Z', 0],
    ['2026-10-01T00:00:00Z', 0], // already due: never negative
  ])('%s is %i day(s) away', (iso, want) => expect(daysLeft(iso, now)).toBe(want));
});

describe('reasonText', () => {
  it('names every reason in words', () => {
    expect(reasonText('removed')).toBe('Removed');
    expect(reasonText('replaced')).toContain('Replaced');
    expect(reasonText('variation_deleted')).toContain('variation was deleted');
  });
});
