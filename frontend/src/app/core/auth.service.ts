import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Router } from '@angular/router';
import { Observable, catchError, map, of, tap } from 'rxjs';
import { LoginResponse, User } from './models';

export type Role = 'admin' | 'designer' | 'production';

interface Session {
  token: string;
  expiresAt: string;
  user: User;
}

const STORAGE_KEY = 'cadportal.session';

/**
 * Signed-in state. The session (token + user) lives in sessionStorage, so it ends
 * with the browser tab. The UI uses the role only to hide controls; the server
 * checks the token and role on every request.
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly http = inject(HttpClient);
  private readonly router = inject(Router);

  private readonly session = signal<Session | null>(read());
  private verified: Promise<boolean> | null = null;

  readonly user = computed(() => this.session()?.user ?? null);
  readonly token = computed(() => this.session()?.token ?? null);
  readonly role = computed<Role | null>(() => this.user()?.role ?? null);
  readonly isAuthenticated = computed(() => this.session() !== null);
  /** Can change designs, seats and CAD files, and see costing. */
  readonly canEdit = computed(() => this.role() === 'admin' || this.role() === 'designer');
  readonly isAdmin = computed(() => this.role() === 'admin');

  login(email: string, password: string): Observable<User> {
    return this.http.post<LoginResponse>('/api/v1/auth/login', { email, password }).pipe(
      tap((r) => this.start(r)),
      map((r) => r.user),
    );
  }

  /** Changing the password invalidates the old token, so the server returns a new one. */
  changePassword(currentPassword: string, newPassword: string): Observable<User> {
    return this.http
      .post<LoginResponse>('/api/v1/auth/change-password', {
        current_password: currentPassword,
        new_password: newPassword,
      })
      .pipe(
        tap((r) => this.start(r)),
        map((r) => r.user),
      );
  }

  /**
   * Confirms the stored session with the server once per page load (a user may have
   * been disabled or had their role changed since the tab last talked to the API).
   */
  verifyOnce(): Promise<boolean> {
    this.verified ??= new Promise<boolean>((resolve) => {
      this.http
        .get<User>('/api/v1/auth/me')
        .pipe(
          tap((u) => this.session.update((s) => (s ? { ...s, user: u } : s))),
          map(() => true),
          catchError(() => of(false)),
        )
        .subscribe((ok) => resolve(ok));
    });
    return this.verified;
  }

  /** Signs out on purpose. */
  logout(): void {
    this.clear();
    void this.router.navigate(['/login']);
  }

  /** The server rejected our token: drop the session and go to the login page. */
  expire(): void {
    if (!this.session()) return;
    const returnUrl = this.router.url;
    this.clear();
    void this.router.navigate(['/login'], {
      queryParams: returnUrl && returnUrl !== '/login' ? { returnUrl } : {},
    });
  }

  private start(r: LoginResponse): void {
    const s: Session = { token: r.token, expiresAt: r.expires_at, user: r.user };
    this.session.set(s);
    this.verified = Promise.resolve(true);
    try {
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(s));
    } catch {
      /* storage unavailable: keep the session in memory only */
    }
  }

  private clear(): void {
    this.session.set(null);
    this.verified = null;
    try {
      sessionStorage.removeItem(STORAGE_KEY);
    } catch {
      /* ignore */
    }
  }
}

function read(): Session | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const s = JSON.parse(raw) as Session;
    if (!s?.token || !s.user?.role || Date.parse(s.expiresAt) <= Date.now()) {
      sessionStorage.removeItem(STORAGE_KEY);
      return null;
    }
    return s;
  } catch {
    return null;
  }
}
