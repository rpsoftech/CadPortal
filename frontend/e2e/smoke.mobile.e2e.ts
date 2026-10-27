import { Page, expect, test } from '@playwright/test';
import { admin } from './auth';
import { seedOrder } from './order-seed';

// A phone (375 px wide). Runs after smoke.e2e.ts, so the catalog has designs to open.
test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

const signIn = admin;

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

const SCREENS: [string, string][] = [
  ['catalog', '/'],
  ['design', '/designs/LR0001'],
  ['variation-editor', '/designs/LR0001/variations/1/edit'],
  ['master-data', '/master-data'],
  ['master-data-matrix', '/master-data?tab=matrix'],
  ['master-data-categories', '/master-data?tab=categories'],
  ['removed', '/removed'],
  ['orders', '/orders'],
  ['order-new-empty', '/orders/new'],
  ['audit', '/audit'],
  ['backups', '/backups'],
  ['users', '/users'],
];

// Other phone and tablet sizes: the smallest phone, a tablet, and a phone turned sideways.
for (const [name, width, height] of [
  ['320 px', 320, 640],
  ['768 px', 768, 1024],
  ['landscape 812x375', 812, 375],
] as const) {
  test.describe(name, () => {
    test.use({ viewport: { width, height } });
    test(`every screen fits ${name} without sideways scrolling`, async ({ page }) => {
      await signIn(page);
      for (const [label, url] of SCREENS) {
        await page.goto(url);
        await check(page, `${label} at ${name}`);
      }
    });
  });
}

test('the catalog filters fold into a panel with a count, and the search stays in view', async ({ page }) => {
  await signIn(page);
  const toggle = page.getByRole('button', { name: /^Filters/ });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByLabel('Net min (g)')).toBeHidden();

  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await page.getByLabel('Net min (g)').fill('1');
  await page.getByLabel('Gross max (g)').fill('9');
  await expect(toggle).toHaveAccessibleName('Filters 2 active');
  await expect(page.getByRole('button', { name: /^Show \d+ design/ })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Clear filters' })).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(toggle).toHaveAccessibleName('Filters');
  await page.getByRole('button', { name: /^Show \d+ design/ }).click();
  await expect(page.getByLabel('Net min (g)')).toBeHidden();

  // the search box stays at the top while the list scrolls
  await page.evaluate(() => window.scrollTo(0, 600));
  await expect(page.getByLabel('Search code or name')).toBeInViewport();
  await check(page, 'catalog scrolled');
});

test('the catalog stone filters and the stone figures fit a phone', async ({ page }) => {
  await signIn(page);
  await page.goto('/?metal=22KY&stone=2HD&pct_min=0&sort=stone_pct_desc');
  await expect(page.locator('mat-card').first()).toContainText(/Stone 2HD|no data for 2HD/);
  await check(page, 'catalog stone figures');
  await page.getByRole('button', { name: /^Filters/ }).click();
  for (const label of ['Stone code', 'Stone % min', 'Stone % max', 'Sort by']) {
    await expect(page.getByLabel(label)).toBeVisible();
  }
  await expect(page.getByLabel('Stone % min')).toHaveAttribute('inputmode', 'decimal');
  const box = await page.locator('mat-form-field', { hasText: 'Stone % min' }).boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(40);
  await check(page, 'catalog stone filters open');
});

test('tables become cards, and the seat grid is one card per seat with large fields', async ({ page }) => {
  await signIn(page);
  for (const [url, label] of [
    ['/users', 'Users'],
    ['/audit', 'Audit log entries'],
    ['/master-data', 'Alloys'],
    ['/master-data?tab=matrix', 'Stone matrix'],
  ]) {
    await page.goto(url);
    const table = page.getByRole('table', { name: label });
    await expect(table.locator('tbody tr').first()).toBeVisible();
    expect(await table.locator('tbody tr').first().evaluate((el) => getComputedStyle(el).display)).toBe('block');
    // a real label sits on each value
    expect(await table.locator('tbody td[data-label]').count()).toBeGreaterThan(2);
  }

  await page.goto('/designs/LR0001/variations/1/edit');
  await page.getByRole('button', { name: 'Add seat' }).click();
  const size = page.getByLabel('Seat 1 size in mm');
  await expect(size).toBeVisible();
  const box = await size.locator('xpath=ancestor::mat-form-field').boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(44);
  expect(box!.width).toBeGreaterThan(120);
  expect(await size.getAttribute('inputmode')).toBe('decimal');
  expect(await page.getByLabel('Seat 1 stone count').getAttribute('inputmode')).toBe('numeric');
  await check(page, 'seat card');
});

test('buttons are finger sized on a phone, and dialogs use the screen width', async ({ page }) => {
  await signIn(page);
  for (const [label, url] of SCREENS) {
    await page.goto(url);
    await page.waitForLoadState('networkidle');
    const small = await page.evaluate(() =>
      Array.from(document.querySelectorAll<HTMLElement>('main button, main a[class*="mat-mdc-button"], main a[class*="mdc-button"]'))
        .filter((el) => {
          const r = el.getBoundingClientRect();
          return r.width > 0 && r.height > 0 && r.height < 43.5 && !el.closest('mat-form-field, mat-chip-set, mat-chip-listbox, mat-slide-toggle, mat-button-toggle-group');
        })
        .map((el) => `${(el.textContent ?? '').trim().slice(0, 24)} ${Math.round(el.getBoundingClientRect().height)}px`),
    );
    expect(small, `${label}: buttons under 44 px`).toEqual([]);
  }
  await page.goto('/');
  await page.getByRole('button', { name: 'New design' }).click();
  const w = (await page.getByRole('dialog').boundingBox())!.width;
  expect(w).toBeGreaterThanOrEqual(375 - 16 - 1);
});

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

  // Costing several designs: the selection bar, the dialog with its preview table, and the saved sheet.
  await page.goto('/');
  await page.getByRole('checkbox', { name: 'Select LR0001' }).check();
  await expect(page.getByText('1 selected')).toBeVisible();
  await check(page, 'catalog-selection');
  await page.getByRole('button', { name: 'Print job bags' }).click();
  await expect(page.getByRole('dialog').getByRole('heading', { name: 'Print job bags for 1 design' })).toBeVisible();
  await check(page, 'print-job-bags-dialog');
  const bagsBox = await page.getByRole('dialog').boundingBox();
  expect(bagsBox!.x + bagsBox!.width).toBeLessThanOrEqual(375 + 1);
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
  await page.getByRole('button', { name: 'Cost selected' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Metal').click();
  await page.getByRole('option', { name: '22KY' }).click();
  await dialog.getByLabel('Stone code').click();
  await page.getByRole('option', { name: '2HD' }).click();
  await dialog.getByLabel('Gold rate / g').fill('6000');
  await dialog.getByLabel('Setting rate / pc').fill('25');
  await dialog.getByRole('button', { name: 'Preview' }).click();
  await expect(dialog.getByRole('table', { name: 'Costing preview' })).toBeVisible();
  await check(page, 'cost-dialog');
  const box = await dialog.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(375 + 1);
  await dialog.getByLabel('Costing sheet name').fill('Mobile check');
  await dialog.getByRole('button', { name: 'Save as costing sheet' }).click();
  await page.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Mobile check' })).toBeVisible();
  await check(page, 'costing-sheet');
  await page.goto('/costing-sheets');
  await expect(page.getByRole('link', { name: 'Mobile check' })).toBeVisible();
  await check(page, 'costing-sheets');
  await page.getByRole('link', { name: 'Mobile check' }).click();
  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('heading', { name: 'Costing sheets' })).toBeVisible();
});

test('the order download buttons wrap on a phone and their dialog fits', async ({ page }) => {
  await signIn(page);
  const o = await seedOrder(page, 'OMobile');
  await page.goto(`/orders/${o.id}`);
  await expect(page.getByRole('table', { name: 'Order lines' })).toBeVisible();
  await check(page, 'order-detail-downloads');
  for (const name of ['Download STL', 'Download 3dm', 'Download package']) {
    const box = await page.getByRole('button', { name }).boundingBox();
    expect(box!.x, name).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width, name).toBeLessThanOrEqual(375 + 1);
    expect(box!.height, name).toBeGreaterThanOrEqual(40);
  }
  await page.getByRole('button', { name: 'Download package' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText(/^2 CAD files /)).toBeVisible();
  const dbox = await dialog.boundingBox();
  expect(dbox!.x).toBeGreaterThanOrEqual(0);
  expect(dbox!.x + dbox!.width).toBeLessThanOrEqual(375 + 1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375);
  const [dl] = await Promise.all([
    page.waitForEvent('download'),
    dialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  expect(dl.suggestedFilename()).toContain(`${o.number}-package-`);
});
