import { describe, expect, it } from 'vitest';
import { ACTION_GROUPS, statusKind } from './audit.component';

describe('statusKind', () => {
  it.each([
    [200, 'ok'],
    [201, 'ok'],
    [204, 'ok'],
    [400, 'rejected'],
    [404, 'rejected'],
    [409, 'rejected'],
    [403, 'denied'],
    [500, 'error'],
    [503, 'error'],
  ] as const)('%i is %s', (status, kind) => expect(statusKind(status)).toBe(kind));
});

describe('ACTION_GROUPS', () => {
  it('starts with "all" and every other prefix ends with a dot', () => {
    expect(ACTION_GROUPS[0].prefix).toBe('');
    for (const g of ACTION_GROUPS.slice(1)) expect(g.prefix).toMatch(/^[a-z_]+\.$/);
  });
  it('covers every action family the API records', () => {
    const prefixes = ACTION_GROUPS.map((g) => g.prefix);
    for (const p of [
      'auth.',
      'user.',
      'metal.',
      'stone_matrix.',
      'design.',
      'variation.',
      'cad.',
      'export.',
    ]) {
      expect(prefixes).toContain(p);
    }
  });
});
