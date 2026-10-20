import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import {
  ActivatedRouteSnapshot,
  Router,
  RouterStateSnapshot,
  UrlTree,
  provideRouter,
} from '@angular/router';
import { beforeEach, describe, expect, it } from 'vitest';
import { authGuard, guestGuard, roleGuard, safeReturnUrl } from './guards';

const state = (url: string) => ({ url }) as RouterStateSnapshot;
const route = {} as ActivatedRouteSnapshot;

function session(role: string) {
  sessionStorage.setItem(
    'cadportal.session',
    JSON.stringify({
      token: 't',
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
      user: {
        id: 1,
        email: 'a@b.co',
        name: '',
        role,
        active: true,
        last_login_at: null,
        created_at: '',
      },
    }),
  );
}

function setup() {
  TestBed.resetTestingModule();
  TestBed.configureTestingModule({
    providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
  });
  return { router: TestBed.inject(Router), ctl: TestBed.inject(HttpTestingController) };
}

describe('safeReturnUrl', () => {
  it.each([
    ['/designs/LR001', '/designs/LR001'],
    ['/designs/LR001?tab=1', '/designs/LR001?tab=1'],
    ['/', '/'],
  ])('keeps %s', (input, want) => expect(safeReturnUrl(input)).toBe(want));

  it.each([
    undefined,
    null,
    '',
    'https://evil.test',
    '//evil.test/x',
    '/\\evil.test',
    'javascript:alert(1)',
    'designs/LR001',
    '/login',
    '/login?returnUrl=/x',
  ])('falls back to / for %s', (input) => expect(safeReturnUrl(input as string)).toBe('/'));
});

describe('guards', () => {
  beforeEach(() => sessionStorage.clear());

  it('authGuard sends a signed-out user to /login with the return url', async () => {
    const { router } = setup();
    const res = (await TestBed.runInInjectionContext(() =>
      authGuard(route, state('/designs/LR001')),
    )) as UrlTree;
    expect(router.serializeUrl(res)).toBe('/login?returnUrl=%2Fdesigns%2FLR001');
  });

  it('authGuard lets a verified session through and redirects a rejected one', async () => {
    session('designer');
    const { ctl } = setup();
    const ok = TestBed.runInInjectionContext(() => authGuard(route, state('/')));
    ctl.expectOne('/api/v1/auth/me').flush({ id: 1, role: 'designer' });
    expect(await ok).toBe(true);

    sessionStorage.clear();
    session('designer');
    const { ctl: ctl2, router } = setup();
    const bad = TestBed.runInInjectionContext(() => authGuard(route, state('/users')));
    ctl2
      .expectOne('/api/v1/auth/me')
      .flush({ error: 'invalid token' }, { status: 401, statusText: 'Unauthorized' });
    expect(router.serializeUrl((await bad) as UrlTree)).toContain('/login');
  });

  it('roleGuard allows only the listed roles', () => {
    session('production');
    const { router } = setup();
    const res = TestBed.runInInjectionContext(() =>
      roleGuard('admin', 'designer')(route, state('/x')),
    );
    expect(router.serializeUrl(res as UrlTree)).toBe('/');

    sessionStorage.clear();
    session('designer');
    setup();
    expect(
      TestBed.runInInjectionContext(() => roleGuard('admin', 'designer')(route, state('/x'))),
    ).toBe(true);
    expect(
      TestBed.runInInjectionContext(() => roleGuard('admin')(route, state('/x'))) instanceof
        UrlTree,
    ).toBe(true);
  });

  it('guestGuard keeps signed-in users off the login page', () => {
    setup();
    expect(TestBed.runInInjectionContext(() => guestGuard(route, state('/login')))).toBe(true);
    session('admin');
    setup();
    expect(
      TestBed.runInInjectionContext(() => guestGuard(route, state('/login'))) instanceof UrlTree,
    ).toBe(true);
  });
});
