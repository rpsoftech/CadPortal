import { TestBed } from '@angular/core/testing';
import { beforeEach, describe, expect, it } from 'vitest';
import { AuthService, decodeToken } from './auth.service';

function token(payload: object): string {
  const b64 = (o: object) =>
    btoa(JSON.stringify(o)).replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_');
  return `${b64({ alg: 'HS256', typ: 'JWT' })}.${b64(payload)}.sig`;
}
const inAnHour = () => Math.floor(Date.now() / 1000) + 3600;

describe('decodeToken', () => {
  it('reads the payload without verifying it', () => {
    expect(decodeToken(token({ role: 'admin', sub: 'u1' }))?.role).toBe('admin');
  });
  it('returns null for garbage', () => {
    expect(decodeToken('nope')).toBeNull();
    expect(decodeToken('a.%%%.c')).toBeNull();
  });
});

describe('AuthService', () => {
  let auth: AuthService;
  beforeEach(() => {
    sessionStorage.clear();
    TestBed.resetTestingModule();
    auth = TestBed.inject(AuthService);
  });

  it('starts signed out', () => {
    expect(auth.token()).toBeNull();
    expect(auth.role()).toBeNull();
    expect(auth.canEdit()).toBe(false);
  });

  it('derives the role and edit rights from the token', () => {
    auth.set(token({ role: 'designer', exp: inAnHour() }));
    expect(auth.role()).toBe('designer');
    expect(auth.canEdit()).toBe(true);
    auth.set(token({ role: 'production', exp: inAnHour() }));
    expect(auth.canEdit()).toBe(false);
  });

  it('accepts a pasted "Bearer " prefix and keeps it per tab only', () => {
    const t = token({ role: 'admin', exp: inAnHour() });
    auth.set(`Bearer ${t}`);
    expect(auth.token()).toBe(t);
    expect(sessionStorage.getItem('cadportal.token')).toBe(t);
    expect(localStorage.getItem('cadportal.token')).toBeNull();
  });

  it('treats an expired token or an unknown role as signed out', () => {
    auth.set(token({ role: 'admin', exp: Math.floor(Date.now() / 1000) - 10 }));
    expect(auth.role()).toBeNull();
    auth.set(token({ role: 'root', exp: inAnHour() }));
    expect(auth.role()).toBeNull();
  });

  it('clear removes the token', () => {
    auth.set(token({ role: 'admin', exp: inAnHour() }));
    auth.clear();
    expect(auth.token()).toBeNull();
    expect(sessionStorage.getItem('cadportal.token')).toBeNull();
  });
});
