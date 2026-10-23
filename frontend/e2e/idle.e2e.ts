import { BrowserContext, Page, expect, test } from '@playwright/test';

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

/**
 * The server's idle timeout is whole minutes, so these tests answer /auth/config themselves with
 * a fraction of a minute (0.1 = 6 s, warning for the last 3 s). The rest of the API is real.
 */
async function shortIdleTimeout(context: BrowserContext, minutes: number): Promise<void> {
  await context.route('**/api/v1/auth/config', async (route) => {
    const res = await route.fetch();
    const body = (await res.json()) as Record<string, unknown>;
    await route.fulfill({ response: res, json: { ...body, idle_timeout_minutes: minutes } });
  });
}

async function signIn(page: Page): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Email').fill(env('E2E_ADMIN_EMAIL'));
  await page.getByLabel('Password').fill(env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
}

test('the real config endpoint carries the idle timeout (30 minutes by default)', async ({
  request,
}) => {
  const res = await request.get('/api/v1/auth/config');
  expect(res.ok()).toBe(true);
  const body = (await res.json()) as { idle_timeout_minutes: number };
  expect(body.idle_timeout_minutes).toBe(30);
});

test('idle: a warning appears, Stay signed in keeps the session, then inactivity signs out', async ({
  page,
  context,
}) => {
  await shortIdleTimeout(context, 0.1);
  await signIn(page);

  // No activity: the warning shows near the end of the period.
  const stay = page.getByRole('button', { name: 'Stay signed in' });
  await expect(stay).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('idle-countdown')).toContainText('You will be signed out in');

  // Only the button answers the warning.
  await stay.click();
  await expect(stay).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();

  // Left alone again: warned, then signed out with a clear message on the login page.
  await expect(stay).toBeVisible({ timeout: 10_000 });
  await expect(page).toHaveURL(/\/login\?.*reason=idle/, { timeout: 10_000 });
  await expect(page.getByTestId('idle-notice')).toContainText(
    'signed out because you were inactive',
  );
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
});

test('idle: activity in one tab keeps the other tab signed in', async ({ page, context }) => {
  await shortIdleTimeout(context, 0.1);
  await signIn(page);
  const session = await page.evaluate(() => sessionStorage.getItem('cadportal.session'));
  expect(session).toBeTruthy();

  // A second tab of the same browser with the same login (sessionStorage is per tab).
  const other = await context.newPage();
  await other.addInitScript(
    (s) => sessionStorage.setItem('cadportal.session', s as string),
    session,
  );
  await other.goto('/');
  await expect(other.getByRole('heading', { name: 'Catalog' })).toBeVisible();

  // Keep using the second tab for longer than the timeout; the first tab is never touched.
  for (let i = 0; i < 14; i++) {
    await other.mouse.move(50 + i * 7, 200 + i * 3);
    await other.waitForTimeout(700);
  }
  await expect(page.getByRole('button', { name: 'Stay signed in' })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();

  // Both tabs go quiet: both are signed out.
  await expect(page).toHaveURL(/reason=idle/, { timeout: 15_000 });
  await expect(other).toHaveURL(/reason=idle/, { timeout: 15_000 });
});
