import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { AUTH_FILE } from './auth';

// Signs in once per E2E user through the API (one request each, not the form) and saves the session
// in the shape the app keeps in sessionStorage. See auth.ts. Without the E2E_* variables (a run of
// only the tests that need none) it does nothing, and `signIn` uses the form.
export default async function globalSetup(): Promise<void> {
  const base = process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080';
  const users = [
    [process.env['E2E_ADMIN_EMAIL'], process.env['E2E_ADMIN_PASSWORD']],
    [process.env['E2E_PROD_EMAIL'], process.env['E2E_PROD_PASSWORD']],
  ];
  const sessions: Record<string, string> = {};
  for (const [email, password] of users) {
    if (!email || !password) continue;
    const res = await fetch(`${base}/api/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
    if (!res.ok) throw new Error(`global setup: signing in ${email} answered ${res.status}`);
    const r = (await res.json()) as { token: string; expires_at: string; user: unknown };
    sessions[email] = JSON.stringify({ token: r.token, expiresAt: r.expires_at, user: r.user });
  }
  mkdirSync(dirname(AUTH_FILE), { recursive: true });
  writeFileSync(AUTH_FILE, JSON.stringify(sessions));
}
