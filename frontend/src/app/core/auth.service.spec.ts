import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthService } from './auth.service';
import { authInterceptor } from './auth.interceptor';
import { LoginResponse, User } from './models';

const user = (role: User['role'] = 'designer'): User => ({
  id: 7,
  email: 'dana@example.com',
  name: 'Dana',
  role,
  active: true,
  last_login_at: null,
  created_at: '2026-10-20T00:00:00Z',
});

const response = (role: User['role'] = 'designer', expiresInMs = 3_600_000): LoginResponse => ({
  token: `tok-${role}`,
  expires_at: new Date(Date.now() + expiresInMs).toISOString(),
  user: user(role),
});

function setup() {
  TestBed.resetTestingModule();
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      provideHttpClient(withInterceptors([authInterceptor])),
      provideHttpClientTesting(),
    ],
  });
  return {
    auth: TestBed.inject(AuthService),
    http: TestBed.inject(HttpTestingController),
    router: TestBed.inject(Router),
  };
}

describe('AuthService', () => {
  beforeEach(() => sessionStorage.clear());

  it('starts signed out', () => {
    const { auth } = setup();
    expect(auth.isAuthenticated()).toBe(false);
    expect(auth.token()).toBeNull();
    expect(auth.role()).toBeNull();
    expect(auth.canEdit()).toBe(false);
    expect(auth.isAdmin()).toBe(false);
  });

  it('logs in, derives the role flags and keeps the session per tab only', () => {
    const { auth, http } = setup();
    let who: User | undefined;
    auth.login('dana@example.com', 'secret').subscribe((u) => (who = u));
    const req = http.expectOne('/api/v1/auth/login');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ email: 'dana@example.com', password: 'secret' });
    req.flush(response('designer'));

    expect(who?.id).toBe(7);
    expect(auth.isAuthenticated()).toBe(true);
    expect(auth.role()).toBe('designer');
    expect(auth.canEdit()).toBe(true);
    expect(auth.isAdmin()).toBe(false);
    expect(sessionStorage.getItem('cadportal.session')).toContain('tok-designer');
    expect(localStorage.getItem('cadportal.session')).toBeNull();
    // The password is never stored.
    expect(sessionStorage.getItem('cadportal.session')).not.toContain('secret');
  });

  it.each([
    ['admin', true, true],
    ['designer', true, false],
    ['production', false, false],
  ] as const)('role %s: canEdit=%s isAdmin=%s', (role, canEdit, isAdmin) => {
    const { auth, http } = setup();
    auth.login('a@b.co', 'x').subscribe();
    http.expectOne('/api/v1/auth/login').flush(response(role));
    expect(auth.canEdit()).toBe(canEdit);
    expect(auth.isAdmin()).toBe(isAdmin);
  });

  it('restores a stored session and drops an expired or malformed one', () => {
    sessionStorage.setItem(
      'cadportal.session',
      JSON.stringify({
        token: 't',
        expiresAt: new Date(Date.now() + 60_000).toISOString(),
        user: user('admin'),
      }),
    );
    expect(setup().auth.role()).toBe('admin');

    sessionStorage.setItem(
      'cadportal.session',
      JSON.stringify({
        token: 't',
        expiresAt: new Date(Date.now() - 1000).toISOString(),
        user: user(),
      }),
    );
    expect(setup().auth.isAuthenticated()).toBe(false);
    expect(sessionStorage.getItem('cadportal.session')).toBeNull();

    sessionStorage.setItem('cadportal.session', '{not json');
    expect(setup().auth.isAuthenticated()).toBe(false);
  });

  it('logout clears the session and goes to the login page', () => {
    const { auth, http, router } = setup();
    const nav = vi.spyOn(router, 'navigate').mockResolvedValue(true);
    auth.login('a@b.co', 'x').subscribe();
    http.expectOne('/api/v1/auth/login').flush(response());
    auth.logout();
    expect(auth.isAuthenticated()).toBe(false);
    expect(sessionStorage.getItem('cadportal.session')).toBeNull();
    expect(nav).toHaveBeenCalledWith(['/login']);
  });

  it('an idle sign-out clears the session and says why on the login page', () => {
    const { auth, http, router } = setup();
    const nav = vi.spyOn(router, 'navigate').mockResolvedValue(true);
    vi.spyOn(router, 'url', 'get').mockReturnValue('/designs/LR0001');
    auth.login('a@b.co', 'x').subscribe();
    http.expectOne('/api/v1/auth/login').flush(response());
    auth.expireIdle();
    expect(auth.isAuthenticated()).toBe(false);
    expect(sessionStorage.getItem('cadportal.session')).toBeNull();
    expect(nav).toHaveBeenCalledWith(['/login'], {
      queryParams: { reason: 'idle', returnUrl: '/designs/LR0001' },
    });
    // Signed out already: nothing more happens.
    nav.mockClear();
    auth.expireIdle();
    expect(nav).not.toHaveBeenCalled();
  });

  it('changing the password swaps in the new token', () => {
    const { auth, http } = setup();
    auth.login('a@b.co', 'x').subscribe();
    http.expectOne('/api/v1/auth/login').flush(response('designer'));
    auth.changePassword('old', 'new password 123').subscribe();
    const req = http.expectOne('/api/v1/auth/change-password');
    expect(req.request.body).toEqual({ current_password: 'old', new_password: 'new password 123' });
    req.flush({ ...response('designer'), token: 'fresh-token' });
    expect(auth.token()).toBe('fresh-token');
  });

  it('verifies the stored session once and refreshes the user from the server', async () => {
    sessionStorage.setItem(
      'cadportal.session',
      JSON.stringify({
        token: 't',
        expiresAt: new Date(Date.now() + 60_000).toISOString(),
        user: user('designer'),
      }),
    );
    const { auth, http } = setup();
    const first = auth.verifyOnce();
    const second = auth.verifyOnce(); // cached: no second request
    http.expectOne('/api/v1/auth/me').flush(user('production')); // role changed server-side
    expect(await first).toBe(true);
    expect(await second).toBe(true);
    expect(auth.role()).toBe('production');
    http.verify();
  });

  it('a rejected session fails verification and ends the session', async () => {
    sessionStorage.setItem(
      'cadportal.session',
      JSON.stringify({
        token: 't',
        expiresAt: new Date(Date.now() + 60_000).toISOString(),
        user: user(),
      }),
    );
    const { auth, http, router } = setup();
    vi.spyOn(router, 'navigate').mockResolvedValue(true);
    const p = auth.verifyOnce();
    http
      .expectOne('/api/v1/auth/me')
      .flush({ error: 'invalid token' }, { status: 401, statusText: 'Unauthorized' });
    expect(await p).toBe(false);
    expect(auth.isAuthenticated()).toBe(false); // the interceptor expired it
  });

  it('keeps the session when the server is unavailable (503 or network error), not only on 401', async () => {
    for (const failure of [
      {
        body: { error: 'service temporarily unavailable' },
        opts: { status: 503, statusText: 'Service Unavailable' },
      },
      { body: null, opts: { status: 0, statusText: 'Unknown Error' } },
    ]) {
      sessionStorage.setItem(
        'cadportal.session',
        JSON.stringify({
          token: 't',
          expiresAt: new Date(Date.now() + 60_000).toISOString(),
          user: user(),
        }),
      );
      const { auth, http } = setup();
      const p = auth.verifyOnce();
      http.expectOne('/api/v1/auth/me').flush(failure.body, failure.opts);
      expect(await p).toBe(true);
      expect(auth.isAuthenticated()).toBe(true);
    }
  });
});
