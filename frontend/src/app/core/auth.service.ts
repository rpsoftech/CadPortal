import { Injectable, computed, signal } from '@angular/core';

export type Role = 'admin' | 'designer' | 'production';

const STORAGE_KEY = 'cadportal.token';

/** Decodes a JWT payload for UI hints only. The server verifies every token. */
export function decodeToken(token: string): { role?: string; exp?: number; sub?: string } | null {
  try {
    const part = token.split('.')[1];
    if (!part) return null;
    const json = atob(part.replace(/-/g, '+').replace(/_/g, '/'));
    return JSON.parse(json);
  } catch {
    return null;
  }
}

/**
 * Holds the bearer token for the session. There is no login endpoint yet, so the
 * user pastes a token; it lives in sessionStorage only (cleared when the tab closes).
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly _token = signal<string | null>(read());
  /** Bumped when the API says authentication is needed, so the shell can prompt. */
  readonly promptRequested = signal(0);

  readonly token = this._token.asReadonly();

  readonly claims = computed(() => {
    const t = this._token();
    return t ? decodeToken(t) : null;
  });

  readonly role = computed<Role | null>(() => {
    const c = this.claims();
    if (!c || (c.exp && c.exp * 1000 < Date.now())) return null;
    return c.role === 'admin' || c.role === 'designer' || c.role === 'production' ? c.role : null;
  });

  /** Can upload CAD files and see costing. */
  readonly canEdit = computed(() => this.role() === 'admin' || this.role() === 'designer');

  set(token: string): void {
    const t = token.trim().replace(/^Bearer\s+/i, '');
    this._token.set(t || null);
    try {
      if (t) sessionStorage.setItem(STORAGE_KEY, t);
      else sessionStorage.removeItem(STORAGE_KEY);
    } catch {
      /* storage unavailable: keep the token in memory only */
    }
  }

  clear(): void {
    this.set('');
  }

  requestSignIn(): void {
    this.promptRequested.update((n) => n + 1);
  }
}

function read(): string | null {
  try {
    return sessionStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}
