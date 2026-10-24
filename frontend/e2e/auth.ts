import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Page, expect } from '@playwright/test';

// Shared sign-in for the browser tests.
//
// The web container rate limits the sign-in form per address (10 a minute, burst 20), and a full
// run used to sign in through the form in almost every test. global-setup.ts now signs in once per
// E2E user through the API and saves the sessions in e2e/.auth/sessions.json; `signIn` hands such a
// session to the page (the app keeps it in sessionStorage, which Playwright's storageState does not
// carry) and only falls back to the real form for any other user or when no saved session exists.
//
// Tests of the sign-in form itself (login errors, forgot password, idle sign-out) keep using the
// form: call `signInViaForm`.

export const SESSION_KEY = 'cadportal.session';
export const AUTH_FILE = join(__dirname, '.auth', 'sessions.json');

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

let cache: Record<string, string> | undefined;

/** The saved session (the JSON the app keeps in sessionStorage) for an E2E user, if one was made. */
function savedSession(email: string, password: string): string | undefined {
  // Only the two E2E users are cached, and only for their own password: a test that changed a
  // password must go through the form.
  const known =
    (email === process.env['E2E_ADMIN_EMAIL'] && password === process.env['E2E_ADMIN_PASSWORD']) ||
    (email === process.env['E2E_PROD_EMAIL'] && password === process.env['E2E_PROD_PASSWORD']);
  if (!known) return undefined;
  if (!cache) cache = existsSync(AUTH_FILE) ? JSON.parse(readFileSync(AUTH_FILE, 'utf8')) : {};
  const s = cache?.[email];
  return s && Date.parse(JSON.parse(s).expiresAt) > Date.now() + 60_000 ? s : undefined;
}

/** Signs in through the sign-in form, exactly as a person does. */
export async function signInViaForm(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
}

/** Signs in with a saved session when there is one, else through the form. Ends on the catalog. */
export async function signIn(page: Page, email: string, password: string): Promise<void> {
  const saved = savedSession(email, password);
  if (!saved) return signInViaForm(page, email, password);
  // Set once per tab: a test that signs out must stay signed out when the page reloads.
  await page.addInitScript(
    ([key, value]) => {
      if (!sessionStorage.getItem(`${key}.injected`)) {
        sessionStorage.setItem(`${key}.injected`, '1');
        sessionStorage.setItem(key, value);
      }
    },
    [SESSION_KEY, saved],
  );
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
}

export const admin = (page: Page) => signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
export const production = (page: Page) => signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
