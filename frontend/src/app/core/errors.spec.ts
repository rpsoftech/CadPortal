import { HttpErrorResponse } from '@angular/common/http';
import { describe, expect, it } from 'vitest';
import { toApiError } from './errors';

const err = (status: number, body: unknown) => new HttpErrorResponse({ status, error: body });

describe('toApiError', () => {
  it('uses the API error message', async () => {
    const e = await toApiError(
      err(409, { error: 'conflict: identical 3dm file already stored on variation LR001-V1' }),
    );
    expect(e.status).toBe(409);
    expect(e.message).toContain('LR001-V1');
  });

  it('parses a JSON error that arrived as a Blob (export endpoints)', async () => {
    const blob = new Blob([
      JSON.stringify({
        error: 'cannot cost',
        metals_missing_wastage: ['18KR'],
        stone_rates_missing: [{ SizeMM: '1.10', Code: '2HD' }],
      }),
    ]);
    const e = await toApiError(err(422, blob));
    expect(e.message).toBe('cannot cost');
    expect(e.metalsMissingWastage).toEqual(['18KR']);
    expect(e.missing).toEqual([{ SizeMM: '1.10', Code: '2HD' }]);
  });

  it('reads matrix gaps from simulate-bom 422s', async () => {
    const e = await toApiError(
      err(422, {
        error: 'missing stone_size_matrix rows',
        missing: [{ SizeMM: '1.50', Code: '1HD' }],
      }),
    );
    expect(e.missing).toEqual([{ SizeMM: '1.50', Code: '1HD' }]);
  });

  it.each([
    [0, 'Cannot reach the API'],
    [401, 'Sign in'],
    [403, 'role is not permitted'],
    [429, 'Too many attempts'],
    [503, 'storage is not configured'],
    [500, 'Request failed (500)'],
  ])('falls back to a readable message for status %i', async (status, fragment) => {
    const e = await toApiError(err(status, null));
    expect(e.message).toContain(fragment);
  });

  it('survives an unparseable blob and non-HTTP errors', async () => {
    expect((await toApiError(err(500, new Blob(['<html>']))).catch(() => null))?.message).toBe(
      'Request failed (500)',
    );
    expect((await toApiError(new Error('boom'))).message).toBe('Unexpected error');
  });
});
