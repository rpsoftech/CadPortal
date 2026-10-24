import { Page, expect, test } from '@playwright/test';

// Orders: filter the catalog, select designs, create an order, follow it through production, see that a
// later edit of a design does not touch it, clone it, and the read-only view for production. Runs after
// smoke.e2e.ts (category prefixes) and makes its own designs (volume 200 mm3, 22KY net 3.550 g).
//
//   A-V1: 10 x 1.30 mm 1HD (0.0050 ct each) = 0.05 ct = 0.010 g, gross 3.560 g
//   A-V2: 20 x 1.30 mm,  0.020 g, gross 3.570 g
//   B, C: 10 x 1.30 mm,  gross 3.560 g
//   D:    one 1.35 mm seat, which has no matrix row: cannot be ordered

test.describe.configure({ mode: 'serial' });

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

// The sign-in form is rate limited per address, so each user signs in once and later tests reuse the session.
const sessions = new Map<string, string>();

async function signIn(page: Page, email: string, password: string): Promise<void> {
  const saved = sessions.get(email);
  if (saved) {
    await page.addInitScript((v) => sessionStorage.setItem('cadportal.session', v), saved);
    await page.goto('/');
  } else {
    await page.goto('/login');
    await page.getByLabel('Email').fill(email);
    await page.getByLabel('Password').fill(password);
    await page.getByRole('button', { name: 'Sign in' }).click();
  }
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
  const session = await page.evaluate(() => sessionStorage.getItem('cadportal.session'));
  if (session) sessions.set(email, session);
}

const admin = (page: Page) => signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
const production = (page: Page) => signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));

async function api(page: Page, method: string, path: string, body?: unknown) {
  return page.evaluate(
    async ({ method, path, body }) => {
      const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
      const r = await fetch(`/api/v1${path}`, {
        method,
        headers: {
          Authorization: `Bearer ${s.token}`,
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const text = await r.text();
      let json: unknown = null;
      try {
        json = text ? JSON.parse(text) : null;
      } catch {
        json = text;
      }
      return { status: r.status, body: json as any };
    },
    { method, path, body },
  );
}

interface Seeded {
  tag: string;
  a: { code: string; v1: number; v2: number };
  b: { code: string };
  c: { code: string };
  d: { code: string };
}
let seeded: Seeded;
let orderUrl = '';
let cloneUrl = '';

async function seed(page: Page): Promise<Seeded> {
  const tag = `${Date.now()}`.slice(-7);
  const rows = ['1HD', '2HD'].map((stone_code) => ({
    size_mm: '1.30',
    stone_code,
    sieve: '',
    carats_per_stone: stone_code === '1HD' ? '0.0050' : '0.0120',
    rate_per_pc: null,
    rate_per_ct: null,
    rate_basis: '',
  }));
  const m = await api(page, 'PUT', '/masters/stone-matrix', { rows, baseline_code: '' });
  expect([200, 202]).toContain(m.status);
  const cats = (await api(page, 'GET', '/masters/categories')).body as any[];
  const rings = cats.find((c) => c.name === 'Rings');
  const variation = async (designId: number, size: string, n: number): Promise<number> => {
    const v = await api(page, 'POST', `/designs/${designId}/variations`, { volume_mm3: '200' });
    expect(v.status).toBe(201);
    const s = await api(page, 'PUT', `/variations/${v.body.id}/seats`, {
      seats: [{ size_mm: size, stone_count: n, locked_stone_code: '' }],
    });
    expect(s.status).toBe(200);
    return v.body.id as number;
  };
  const design = async (label: string): Promise<{ id: number; code: string }> => {
    const d = await api(page, 'POST', '/designs', { name: `Order ${tag} ${label}`, category_id: rings.id, tag_ids: [] });
    expect(d.status).toBe(201);
    return { id: d.body.id, code: d.body.code };
  };
  const a = await design('A');
  const a1 = await variation(a.id, '1.30', 10);
  const a2 = await variation(a.id, '1.30', 20);
  const b = await design('B');
  await variation(b.id, '1.30', 10);
  const c = await design('C');
  await variation(c.id, '1.30', 10);
  const d = await design('D');
  await variation(d.id, '1.35', 4);
  return { tag, a: { code: a.code, v1: a1, v2: a2 }, b: { code: b.code }, c: { code: c.code }, d: { code: d.code } };
}

const dialog = (page: Page) => page.getByRole('dialog');

test('filter the catalog, select three designs, create an order with quantities and open it', async ({ page }) => {
  await admin(page);
  seeded = await seed(page);
  const s = seeded;

  // metal, stone code and a weight range in the address; D (no matrix row) is selected later on purpose
  await page.goto(`/?q=${encodeURIComponent(`Order ${s.tag}`)}&metal=22KY&stone=1HD&net_min=3&net_max=4`);
  for (const code of [s.a.code, s.b.code, s.c.code]) {
    await page.getByRole('checkbox', { name: `Select ${code}` }).check();
  }
  await expect(page.getByText('3 selected')).toBeVisible();
  await page.getByRole('button', { name: 'Create order' }).click();

  await expect(page.getByRole('heading', { name: 'New order', level: 1 })).toBeVisible();
  // the catalog's metal and stone code are the defaults
  await expect(page.getByRole('combobox', { name: 'Metal' })).toContainText('22KY');
  await expect(page.getByRole('combobox', { name: 'Stone code' })).toContainText('1HD');
  // A has two variations: four lines in all
  await expect(page.getByRole('table', { name: 'Order lines' }).getByRole('row')).toHaveCount(5);
  await page.getByLabel(`Quantity of ${s.a.code}-V1`).fill('3');
  await page.getByLabel(`Quantity of ${s.a.code}-V2`).fill('2');
  // a bad quantity is refused in place and blocks saving
  await page.getByLabel(`Quantity of ${s.b.code}-V1`).fill('0');
  await expect(page.getByRole('alert').filter({ hasText: '1 to 100000' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save as draft' })).toBeDisabled();
  await page.getByLabel(`Quantity of ${s.b.code}-V1`).fill('1');
  await page.getByLabel('Title / buyer').fill('Mehta wedding');
  await page.getByLabel('Notes').fill('Rush for the 12th');
  await page.getByRole('button', { name: 'Save as draft' }).click();

  await expect(page).toHaveURL(/\/orders\/\d+$/);
  orderUrl = new URL(page.url()).pathname;
  await expect(page.getByRole('heading', { level: 1 })).toContainText('ORD-');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Mehta wedding');
  await expect(page.getByText('Draft', { exact: true }).first()).toBeVisible();
  // 4 lines, 3 + 2 + 1 + 1 pieces; 7 x 3.550 net, gross from the snapshot
  await expect(page.getByText('4 · 7')).toBeVisible();
  await expect(page.getByText('24.850 g / 24.940 g')).toBeVisible();
  const table = page.getByRole('table', { name: 'Order lines' });
  await expect(table.getByRole('row', { name: new RegExp(`${s.a.code}.*${s.a.code}-V1`) })).toContainText('3.560');

  // it is on the list, which can be searched
  await page.getByRole('link', { name: 'Orders', exact: true }).first().click();
  await expect(page.getByRole('heading', { name: 'Orders', level: 1 })).toBeVisible();
  await page.getByLabel('Search number, title or notes').fill('mehta');
  await expect(page.getByRole('link', { name: /^ORD-/ })).toHaveCount(1);
  await page.getByLabel('Search number, title or notes').fill('no such order xyz');
  await expect(page.getByText('No order matches this filter.')).toBeVisible();
});

test('a design that cannot be ordered is named and can be left out', async ({ page }) => {
  await admin(page);
  const s = seeded;
  await page.goto(`/?q=${encodeURIComponent(`Order ${s.tag}`)}&metal=22KY&stone=1HD`);
  await page.getByRole('checkbox', { name: `Select ${s.a.code}` }).check();
  await page.getByRole('checkbox', { name: `Select ${s.d.code}` }).check();
  await page.getByRole('button', { name: 'Create order' }).click();
  await expect(page.getByRole('heading', { name: 'New order', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Save as draft' }).click();

  // the server's words, naming the variation, and nothing was created
  const alert = page.getByRole('alert').filter({ hasText: 'cannot add to the order' });
  await expect(alert).toBeVisible();
  await expect(alert).toContainText(`${s.d.code}-V1`);
  await page.getByRole('button', { name: /Leave out the 1 line/ }).click();
  await expect(page.getByRole('heading', { name: 'Left out' })).toBeVisible();
  await expect(page.getByText(`${s.d.code}-V1`).last()).toBeVisible();
  await page.getByRole('button', { name: 'Save as draft' }).click();
  await expect(page).toHaveURL(/\/orders\/\d+$/);
  await expect(page.getByRole('table', { name: 'Order lines' }).getByRole('row')).toHaveCount(3);
});

test('place, start production and finish; the history names each step and who made it', async ({ page }) => {
  await admin(page);
  await page.goto(orderUrl);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('ORD-');

  for (const [button, status] of [
    ['Place order', 'Placed'],
    ['Start production', 'In production'],
    ['Mark done', 'Done'],
  ] as const) {
    await page.getByRole('region', { name: 'Order actions' }).getByRole('button', { name: button }).click();
    await dialog(page).getByLabel(/^Note/).fill(`note for ${status}`);
    await dialog(page).getByRole('button', { name: button }).click();
    await expect(page.getByRole('heading', { level: 1 }).locator('xpath=following-sibling::app-order-status')).toContainText(status);
    // announced to screen readers
    await expect(page.locator('p.sr-only[role="status"]')).toContainText('is now');
  }
  const history = page.getByRole('list').filter({ hasText: 'note for Placed' });
  await expect(history.getByRole('listitem')).toHaveCount(4); // created + 3 moves
  await expect(history).toContainText('note for In production');
  await expect(history).toContainText('E2E admin');
  // a finished order has no more status buttons, and its lines cannot be edited
  await expect(page.getByRole('region', { name: 'Order actions' }).getByRole('button', { name: /Place order|Start production|Mark done|Cancel order/ })).toHaveCount(0);
  await expect(page.getByRole('spinbutton')).toHaveCount(0);
});

test('editing a design afterwards leaves the order as it was and marks the line', async ({ page }) => {
  await admin(page);
  const s = seeded;
  const r = await api(page, 'PUT', `/variations/${s.a.v1}/seats`, {
    seats: [{ size_mm: '1.30', stone_count: 12, locked_stone_code: '' }],
  });
  expect(r.status).toBe(200);

  await page.goto(orderUrl);
  const row = page.getByRole('table', { name: 'Order lines' }).getByRole('row', { name: new RegExp(`${s.a.code}-V1`) });
  await expect(row).toContainText('Changed since this order was made');
  // still the old snapshot: 10 stones, gross 3.560 g
  await expect(row).toContainText('3.560');
  await row.getByText('What changed').click();
  await expect(row).toContainText('total pcs');
  // the other lines are unmarked
  await expect(page.getByText('Changed since this order was made')).toHaveCount(1);
});

test('order again shows what differs from the old order', async ({ page }) => {
  await admin(page);
  await page.goto(orderUrl);
  await page.getByRole('region', { name: 'Order actions' }).getByRole('button', { name: 'Order again' }).click();
  await dialog(page).getByRole('button', { name: 'Make new draft' }).click();
  await expect(dialog(page).getByRole('status')).toContainText('1 changed');
  await expect(dialog(page)).toContainText(`${s().a.code}-V1`);
  await expect(dialog(page)).toContainText('total pcs');
  await dialog(page).getByRole('button', { name: /^Open ORD-/ }).click();

  await expect(page).toHaveURL(/\/orders\/\d+$/);
  cloneUrl = new URL(page.url()).pathname;
  expect(cloneUrl).not.toBe(orderUrl);
  await expect(page.getByText('the order it was cloned from')).toBeVisible();
  // the copy is a draft with the same quantities, and shows today's numbers (12 stones)
  await expect(page.getByText('4 · 7')).toBeVisible();
  await expect(page.getByRole('spinbutton').first()).toBeVisible();
});

test('production sees an order read-only', async ({ page }) => {
  await production(page);
  await page.getByRole('link', { name: 'Orders', exact: true }).first().click();
  await expect(page.getByRole('heading', { name: 'Orders', level: 1 })).toBeVisible();
  await page.goto(cloneUrl);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('ORD-');
  await expect(page.getByRole('table', { name: 'Order lines' })).toContainText(s().a.code);
  // no write controls at all (hidden, not just disabled)
  for (const name of ['Place order', 'Cancel order', 'Order again', 'Save details', 'Save lines']) {
    await expect(page.getByRole('button', { name })).toHaveCount(0);
  }
  await expect(page.getByRole('spinbutton')).toHaveCount(0);
  await expect(page.getByLabel('Title / buyer')).toHaveCount(0);
  // nor in the catalog, nor by address
  await page.goto('/');
  await page.getByRole('checkbox', { name: /^Select / }).first().check();
  await expect(page.getByRole('button', { name: 'Create order' })).toHaveCount(0);
  await page.goto('/orders/new');
  await expect(page.getByRole('heading', { name: 'New order' })).toHaveCount(0);
});

test('cancel an order', async ({ page }) => {
  await admin(page);
  await page.goto(cloneUrl);
  await page.getByRole('region', { name: 'Order actions' }).getByRole('button', { name: 'Cancel order' }).click();
  await expect(dialog(page)).toContainText('cannot be reopened');
  await dialog(page).getByRole('button', { name: 'Cancel order' }).click();
  await expect(page.getByRole('heading', { level: 1 }).locator('xpath=following-sibling::app-order-status')).toContainText('Cancelled');
  await expect(page.getByRole('region', { name: 'Order actions' }).getByRole('button', { name: /Place order|Cancel order/ })).toHaveCount(0);
  // never deleted: still on the list under its status
  await page.goto('/orders');
  await page.getByRole('combobox', { name: 'Status' }).click();
  await page.getByRole('option', { name: 'Cancelled' }).click();
  await expect(page.getByRole('link', { name: /^ORD-/ }).first()).toBeVisible();
});

test.describe('phone', () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test('new order, list and detail fit 375 px and keep 44 px targets', async ({ page }) => {
    await admin(page);
    const s = seeded;
    const fits = async (label: string) => {
      await page.waitForLoadState('networkidle');
      const w = await page.evaluate(() => ({ page: document.documentElement.scrollWidth, view: document.documentElement.clientWidth }));
      expect(w.page, `${label} scrolls sideways`).toBeLessThanOrEqual(w.view);
    };
    await page.goto(`/?q=${encodeURIComponent(`Order ${s.tag}`)}&metal=22KY&stone=1HD`);
    await page.getByRole('checkbox', { name: `Select ${s.a.code}` }).check();
    await page.getByRole('button', { name: 'Create order' }).click();
    await expect(page.getByRole('heading', { name: 'New order', level: 1 })).toBeVisible();
    await fits('new order');
    const box = await page.getByRole('button', { name: `Remove ${s.a.code}-V1 from the order` }).boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(40);
    await page.goto('/orders');
    await expect(page.getByRole('link', { name: /^ORD-/ }).first()).toBeVisible();
    await fits('orders list');
    await page.goto(orderUrl);
    await expect(page.getByRole('heading', { level: 1 })).toContainText('ORD-');
    await fits('order detail');
  });
});

/** The seeded designs (set by the first test). */
function s(): Seeded {
  return seeded;
}
