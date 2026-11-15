import { HttpClient, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { Router, provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthService } from './auth.service';
import { ACTIVITY_KEY, IdleService } from './idle.service';
import { LoginResponse } from './models';

const login: LoginResponse = {
  token: 't',
  expires_at: new Date(Date.now() + 3_600_000).toISOString(),
  user: {
    id: 1,
    email: 'a@b.co',
    name: 'A',
    role: 'designer',
    active: true,
    last_login_at: null,
    created_at: '2026-10-20T00:00:00Z',
  },
};

function setup() {
  TestBed.resetTestingModule();
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      provideHttpClient(),
      provideHttpClientTesting(),
      { provide: MatDialog, useValue: { open: vi.fn(() => ({ close: vi.fn() })) } },
    ],
  });
  const idle = TestBed.inject(IdleService);
  const auth = TestBed.inject(AuthService);
  const http = TestBed.inject(HttpTestingController);
  const dialog = TestBed.inject(MatDialog) as unknown as { open: ReturnType<typeof vi.fn> };
  vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
  return { idle, auth, http, dialog };
}

/** The user does something partway through an interval of `ms`, and then the interval's timer fires. */
function activeDuring(ms: number) {
  vi.advanceTimersByTime(1000);
  document.dispatchEvent(new Event('keydown'));
  vi.advanceTimersByTime(ms - 1000);
}

/** Signs in and lets the effect fetch the config. */
async function signIn(ctx: ReturnType<typeof setup>, minutes: number | undefined) {
  ctx.auth.login('a@b.co', 'x').subscribe();
  ctx.http.expectOne('/api/v1/auth/login').flush(login);
  TestBed.tick();
  const cfg = ctx.http.expectOne('/api/v1/auth/config');
  cfg.flush(
    minutes === undefined ? { password_reset_email: false } : { idle_timeout_minutes: minutes },
  );
}

describe('IdleService', () => {
  beforeEach(() => {
    sessionStorage.clear();
    localStorage.clear();
    vi.useFakeTimers();
  });
  afterEach(() => vi.useRealTimers());

  it('warns, then signs out after the idle period from the server', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    vi.advanceTimersByTime(4 * 60_000 + 1000);
    expect(ctx.dialog.open).toHaveBeenCalledTimes(1);
    expect(ctx.idle.secondsLeft()).toBeLessThanOrEqual(60);
    expect(ctx.auth.isAuthenticated()).toBe(true);
    vi.advanceTimersByTime(61_000);
    expect(ctx.auth.isAuthenticated()).toBe(false);
    expect(ctx.idle.secondsLeft()).toBeNull();
  });

  it('keyboard and pointer activity keeps the user signed in; the dialog is not shown', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    for (let i = 0; i < 8; i++) {
      vi.advanceTimersByTime(60_000);
      document.dispatchEvent(new Event('keydown'));
    }
    expect(ctx.dialog.open).not.toHaveBeenCalled();
    expect(ctx.auth.isAuthenticated()).toBe(true);
  });

  it('a request the app makes on its own is not activity', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    const client = TestBed.inject(HttpClient);
    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(60_000);
      client.get('/api/v1/designs').subscribe({ error: () => undefined }); // a poll, not a person
      ctx.http.expectOne('/api/v1/designs').flush([]);
    }
    expect(ctx.auth.isAuthenticated()).toBe(false);
  });

  it('activity in another tab (localStorage) keeps this tab signed in', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    vi.advanceTimersByTime(3.5 * 60_000);
    localStorage.setItem(ACTIVITY_KEY, String(Date.now()));
    window.dispatchEvent(new StorageEvent('storage', { key: ACTIVITY_KEY }));
    vi.advanceTimersByTime(3 * 60_000);
    expect(ctx.auth.isAuthenticated()).toBe(true);
    expect(ctx.dialog.open).not.toHaveBeenCalled();
  });

  it('stay() extends the session', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    vi.advanceTimersByTime(4 * 60_000 + 5000);
    expect(ctx.dialog.open).toHaveBeenCalled();
    ctx.idle.stay();
    vi.advanceTimersByTime(3 * 60_000);
    expect(ctx.auth.isAuthenticated()).toBe(true);
  });

  it('tells the server "still here" while the user is active, every few minutes', async () => {
    const ctx = setup();
    await signIn(ctx, 30); // pings every 5 minutes
    for (let i = 0; i < 3; i++) {
      activeDuring(5 * 60_000);
      const ping = ctx.http.expectOne('/api/v1/auth/me');
      expect(ping.request.method).toBe('GET');
      ping.flush(login.user);
    }
  });

  it('sends nothing while the user is away, so the server can end the login', async () => {
    const ctx = setup();
    await signIn(ctx, 30);
    activeDuring(5 * 60_000); // one burst of activity, then nothing
    ctx.http.expectOne('/api/v1/auth/me').flush(login.user); // it falls in the first interval
    vi.advanceTimersByTime(12 * 60_000); // two more intervals with nobody there
    ctx.http.expectNone('/api/v1/auth/me');
  });

  it('"Stay signed in" tells the server at once', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    vi.advanceTimersByTime(4 * 60_000 + 5000);
    expect(ctx.dialog.open).toHaveBeenCalled();
    ctx.http.expectNone('/api/v1/auth/me');
    ctx.idle.stay();
    ctx.http.expectOne('/api/v1/auth/me').flush(login.user);
  });

  it('a failed ping is ignored; the next one tries again', async () => {
    const ctx = setup();
    await signIn(ctx, 30);
    activeDuring(5 * 60_000);
    ctx.http
      .expectOne('/api/v1/auth/me')
      .flush({ error: 'down' }, { status: 503, statusText: 'Unavailable' });
    expect(ctx.auth.isAuthenticated()).toBe(true);
    activeDuring(5 * 60_000);
    ctx.http.expectOne('/api/v1/auth/me').flush(login.user);
  });

  it('stops pinging after sign-out', async () => {
    const ctx = setup();
    await signIn(ctx, 30);
    ctx.auth.logout();
    TestBed.tick();
    ctx.http.expectOne('/api/v1/auth/logout').flush(null, { status: 204, statusText: 'No Content' });
    activeDuring(5 * 60_000);
    vi.advanceTimersByTime(30 * 60_000);
    ctx.http.expectNone('/api/v1/auth/me');
  });

  it.each([0, undefined])('does nothing when the server says %s', async (minutes) => {
    const ctx = setup();
    await signIn(ctx, minutes);
    vi.advanceTimersByTime(24 * 3_600_000);
    expect(ctx.auth.isAuthenticated()).toBe(true);
    expect(ctx.dialog.open).not.toHaveBeenCalled();
  });

  it('still works when localStorage throws', async () => {
    const ctx = setup();
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    try {
      await signIn(ctx, 2);
      vi.advanceTimersByTime(2 * 60_000 + 1000);
      expect(ctx.auth.isAuthenticated()).toBe(false);
    } finally {
      get.mockRestore();
      set.mockRestore();
    }
  });

  it('stops watching after sign-out', async () => {
    const ctx = setup();
    await signIn(ctx, 5);
    ctx.auth.logout();
    TestBed.tick();
    vi.advanceTimersByTime(10 * 60_000);
    expect(ctx.dialog.open).not.toHaveBeenCalled();
  });
});
