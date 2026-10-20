import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { authInterceptor } from './auth.interceptor';
import { AuthService } from './auth.service';

function setup() {
  sessionStorage.setItem(
    'cadportal.session',
    JSON.stringify({
      token: 'the-token',
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
      user: {
        id: 1,
        email: 'a@b.co',
        name: '',
        role: 'designer',
        active: true,
        last_login_at: null,
        created_at: '',
      },
    }),
  );
  TestBed.resetTestingModule();
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      provideHttpClient(withInterceptors([authInterceptor])),
      provideHttpClientTesting(),
    ],
  });
  const router = TestBed.inject(Router);
  const nav = vi.spyOn(router, 'navigate').mockResolvedValue(true);
  return {
    http: TestBed.inject(HttpClient),
    ctl: TestBed.inject(HttpTestingController),
    auth: TestBed.inject(AuthService),
    nav,
  };
}

describe('authInterceptor', () => {
  beforeEach(() => sessionStorage.clear());

  it('adds the bearer token to API calls only', () => {
    const { http, ctl } = setup();
    http.get('/api/v1/designs').subscribe();
    expect(ctl.expectOne('/api/v1/designs').request.headers.get('Authorization')).toBe(
      'Bearer the-token',
    );
    // The token never goes to another origin.
    http.get('https://other.example/api/v1/x').subscribe();
    expect(
      ctl.expectOne('https://other.example/api/v1/x').request.headers.has('Authorization'),
    ).toBe(false);
    http.get('/assets/logo.svg').subscribe();
    expect(ctl.expectOne('/assets/logo.svg').request.headers.has('Authorization')).toBe(false);
  });

  it('a 401 ends the session and sends the user to the login page', () => {
    const { http, ctl, auth, nav } = setup();
    http.get('/api/v1/designs').subscribe({ error: () => undefined });
    ctl
      .expectOne('/api/v1/designs')
      .flush({ error: 'invalid token' }, { status: 401, statusText: 'Unauthorized' });
    expect(auth.isAuthenticated()).toBe(false);
    expect(nav).toHaveBeenCalledTimes(1);
    expect(nav.mock.calls[0][0]).toEqual(['/login']);
  });

  it('a 401 from the login call itself (wrong password) does not touch the session', () => {
    const { http, ctl, auth, nav } = setup();
    http.post('/api/v1/auth/login', {}).subscribe({ error: () => undefined });
    ctl
      .expectOne('/api/v1/auth/login')
      .flush({ error: 'bad' }, { status: 401, statusText: 'Unauthorized' });
    expect(auth.isAuthenticated()).toBe(true);
    expect(nav).not.toHaveBeenCalled();
  });

  it('403 and other errors keep the session', () => {
    const { http, ctl, auth, nav } = setup();
    http.get('/api/v1/users').subscribe({ error: () => undefined });
    ctl
      .expectOne('/api/v1/users')
      .flush({ error: 'role not permitted' }, { status: 403, statusText: 'Forbidden' });
    expect(auth.isAuthenticated()).toBe(true);
    expect(nav).not.toHaveBeenCalled();
  });
});
