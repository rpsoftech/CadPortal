import { describe, expect, it } from 'vitest';
import { fullLink } from './reset-link-dialog.component';

const link = { path: '/reset-password?token=abc_DEF-123', expires_at: '2026-10-22T12:00:00Z' };

describe('fullLink', () => {
  it('puts the portal address in front of the path', () => {
    expect(fullLink('https://cad.example.com', link)).toBe(
      'https://cad.example.com/reset-password?token=abc_DEF-123',
    );
  });
  it('does not double the slash', () => {
    expect(fullLink('https://cad.example.com/', link)).toBe(
      'https://cad.example.com/reset-password?token=abc_DEF-123',
    );
  });
  it('keeps a port', () => {
    expect(fullLink('http://127.0.0.1:8080', link)).toBe(
      'http://127.0.0.1:8080/reset-password?token=abc_DEF-123',
    );
  });
});
