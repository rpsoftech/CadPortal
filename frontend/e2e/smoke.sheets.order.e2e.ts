import { readFileSync } from 'node:fs';
import { Page, expect, test } from '@playwright/test';
import { PDFDocument } from 'pdf-lib';
import { signIn } from './auth';

// What an order produces: the summary PDF, job bags, CSV and "Cost this order" on the order detail.
// Runs after smoke.sheets.e2e.ts (file order) and sets up its own wastage, stone price and designs.

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

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
      return { status: r.status, body: text ? JSON.parse(text) : null };
    },
    { method, path, body },
  );
}

let orderId = 0;

async function pdfPages(path: string): Promise<number> {
  const bytes = readFileSync(path);
  expect(bytes.subarray(0, 5).toString('latin1')).toBe('%PDF-');
  return (await PDFDocument.load(bytes)).getPageCount();
}

async function price2(page: Page): Promise<void> {
  const r = await api(page, 'PUT', '/masters/stone-matrix', {
    rows: [{ size_mm: '1.10', stone_code: '2HD', sieve: '', carats_per_stone: '0.0070', rate_per_pc: '100', rate_per_ct: null, rate_basis: 'pc' }],
    baseline_code: '',
  });
  expect([200, 202]).toContain(r.status);
}

async function download(page: Page, button: string): Promise<string> {
  const d = page.waitForEvent('download');
  await page.getByRole('button', { name: button, exact: true }).click();
  return (await (await d).path())!;
}

test('an order prints its summary and job bags, exports a CSV and is costed for its quantities', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));

  // 22KY with 10 % wastage, 2HD priced 100 per piece at 1.10 mm; designs of 200 and 100 mm3.
  const metals = (await api(page, 'GET', '/masters/metals')).body as any[];
  const m = metals.find((x) => x.code === '22KY');
  expect(
    (
      await api(page, 'PUT', `/masters/metals/${m.id}`, {
        specific_gravity: m.specific_gravity,
        wastage_pct: '10',
        is_default_input: m.is_default_input,
        active: m.active,
      })
    ).status,
  ).toBe(200);
  const price = await api(page, 'PUT', '/masters/stone-matrix', {
    rows: [{ size_mm: '1.10', stone_code: '2HD', sieve: '', carats_per_stone: '0.0070', rate_per_pc: '100', rate_per_ct: null, rate_basis: 'pc' }],
    baseline_code: '',
  });
  expect([200, 202]).toContain(price.status);
  const cats = (await api(page, 'GET', '/masters/categories')).body as any[];
  const rings = cats.find((c) => c.name === 'Rings');
  const tag = `${Date.now()}`.slice(-7);
  const make = async (label: string, volume: string, n: number) => {
    const d = await api(page, 'POST', '/designs', { name: `OOut ${tag} ${label}`, category_id: rings.id, tag_ids: [] });
    const v = await api(page, 'POST', `/designs/${d.body.id}/variations`, { volume_mm3: volume });
    const s = await api(page, 'PUT', `/variations/${v.body.id}/seats`, { seats: [{ size_mm: '1.10', stone_count: n, locked_stone_code: '' }] });
    expect(s.status).toBe(200);
    return v.body.id as number;
  };
  const va = await make('A', '200', 24);
  const vb = await make('B', '100', 10);
  const order = (
    await api(page, 'POST', '/orders', {
      name: `Outputs ${tag}`, metal_code: '22KY', stone_code: '2HD',
      items: [{ variation_id: va, quantity: 3 }, { variation_id: vb, quantity: 2 }],
    })
  ).body;

  orderId = order.id;
  await page.goto(`/orders/${order.id}`);
  await expect(page.getByRole('table', { name: 'Order lines' })).toBeVisible();

  expect(await pdfPages(await download(page, 'Summary PDF'))).toBe(1);
  expect(await pdfPages(await download(page, 'Job bags'))).toBe(2);
  expect(await pdfPages(await download(page, 'Job bags, one per piece'))).toBe(5);
  const csv = readFileSync(await download(page, 'Order CSV'), 'utf8').trim().split('\n');
  expect(csv).toHaveLength(4); // header, two lines, TOTAL
  expect(csv[0]).toMatch(/^order,design,/);
  expect(csv[3]).toContain(',TOTAL,');

  // Cost this order: A 3 x (23430 gold + 2400 stones + 600 setting) + B 2 x (11715 + 1000 + 250).
  await page.getByRole('button', { name: 'Cost this order' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Gold rate / g').fill('6000');
  await dialog.getByLabel('Setting rate / pc').fill('25');
  await dialog.getByRole('button', { name: 'Cost this order' }).click();
  await expect(dialog.getByText(/total 105220\.00 for 2 lines/)).toBeVisible();
  await expect(dialog.getByText('This order no longer matches the designs.')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Open the sheet' }).click();
  await expect(page).toHaveURL(/\/costing-sheets\/\d+$/);
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Costing sheets' })).toBeVisible();
  await expect(page.getByRole('link', { name: `${order.number} costing` })).toBeVisible();

  // Missing data is never costed as zero: take the price away and the dialog lists what is missing.
  await api(page, 'PUT', '/masters/stone-matrix', {
    rows: [{ size_mm: '1.10', stone_code: '2HD', sieve: '', carats_per_stone: '0.0070', rate_per_pc: null, rate_per_ct: null, rate_basis: '' }],
    baseline_code: '',
  });
  await page.getByRole('button', { name: 'Cost this order' }).click();
  await dialog.getByLabel('Gold rate / g').fill('6000');
  await dialog.getByLabel('Setting rate / pc').fill('25');
  await dialog.getByRole('button', { name: 'Cost this order' }).click();
  await expect(dialog.getByRole('alert')).toContainText('cannot cost');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await price2(page); // leave the price as later tests expect it
});

test('production prints the order summary and job bags but never sees costing', async ({ page }) => {
  expect(orderId).toBeGreaterThan(0); // made by the test above
  await signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  await page.goto(`/orders/${orderId}`);
  await expect(page.getByRole('table', { name: 'Order lines' })).toBeVisible();
  expect(await pdfPages(await download(page, 'Summary PDF'))).toBe(1);
  expect(await pdfPages(await download(page, 'Job bags'))).toBe(2);
  await expect(page.getByRole('button', { name: 'Cost this order' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Costing sheets' })).toHaveCount(0);
});
