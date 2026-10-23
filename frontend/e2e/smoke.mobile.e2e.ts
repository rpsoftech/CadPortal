import { Page, expect, test } from '@playwright/test';

// A phone (375 px wide). Runs after smoke.e2e.ts, so the catalog has designs to open.
test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

async function signIn(page: Page): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Email').fill(env('E2E_ADMIN_EMAIL'));
  await page.getByLabel('Password').fill(env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
}

/** Elements that stick out past the right edge of the screen (what makes a page scroll sideways). */
async function overflowing(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const w = document.documentElement.clientWidth;
    const out: string[] = [];
    for (const el of Array.from(document.body.querySelectorAll('*'))) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      if (r.right > w + 1 && !el.closest('.cdk-overlay-container')) {
        const text = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 30);
        out.push(`${el.tagName.toLowerCase()}.${(el.getAttribute('class') ?? '').split(' ')[0]} right=${Math.round(r.right)} "${text}"`);
      }
    }
    return out.slice(0, 8);
  });
}

async function check(page: Page, label: string): Promise<void> {
  await page.waitForLoadState('networkidle');
  if (process.env['E2E_SHOTS']) {
    await page.screenshot({ path: `test-results/mobile-${label}.png`, fullPage: true });
  }
  const scroll = await page.evaluate(() => ({
    page: document.documentElement.scrollWidth,
    view: document.documentElement.clientWidth,
  }));
  // The whole page must not scroll sideways. A wide table or tab strip may scroll inside its own box.
  const culprits = (await overflowing(page)).join(' | ');
  expect(scroll.page, `${label} scrolls sideways: ${culprits}`).toBeLessThanOrEqual(scroll.view);
}

test('every screen fits a phone without sideways scrolling', async ({ page }) => {
  await signIn(page);
  await check(page, 'catalog');
  await page.getByRole('button', { name: 'Menu', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: 'Backups' })).toBeVisible();
  await page.keyboard.press('Escape');

  await page.goto('/designs/LR0001');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await check(page, 'design');
  await page.getByRole('link', { name: 'Edit seats & files' }).click();
  await page.waitForLoadState('networkidle');
  await check(page, 'variation-editor');

  for (const [label, url] of [
    ['master-data', '/master-data'],
    ['master-data-matrix', '/master-data?tab=matrix'],
    ['master-data-categories', '/master-data?tab=categories'],
    ['removed', '/removed'],
    ['audit', '/audit'],
    ['backups', '/backups'],
    ['users', '/users'],
  ]) {
    await page.goto(url);
    await check(page, label);
  }
});
