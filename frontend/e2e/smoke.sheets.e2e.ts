import { readFileSync } from 'node:fs';
import { Page, expect, test } from '@playwright/test';

// Costing several designs at once, and saved costing sheets. Runs after smoke.e2e.ts (it needs the
// category prefixes that test enters) and sets up its own stone prices, wastage and designs.

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

async function signIn(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
}

/** A call to the API with the signed-in user's token, as the app itself makes them. */
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

async function setWastage(page: Page, metal: string, pct: string): Promise<void> {
  const metals = (await api(page, 'GET', '/masters/metals')).body as any[];
  const m = metals.find((x) => x.code === metal);
  const r = await api(page, 'PUT', `/masters/metals/${m.id}`, {
    specific_gravity: m.specific_gravity,
    wastage_pct: pct,
    is_default_input: m.is_default_input,
    active: m.active,
  });
  expect(r.status).toBe(200);
}

async function setPrice(page: Page, size: string, ct: string, perPc: string | null): Promise<void> {
  const r = await api(page, 'PUT', '/masters/stone-matrix', {
    rows: [
      {
        size_mm: size,
        stone_code: '2HD',
        sieve: '',
        carats_per_stone: ct,
        rate_per_pc: perPc,
        rate_per_ct: null,
        rate_basis: perPc === null ? '' : 'pc',
      },
    ],
    baseline_code: '',
  });
  expect(r.status).toBe(200);
}

interface Seeded {
  tag: string;
  a: { id: number; code: string; name: string };
  b: { id: number; code: string; name: string };
  c: { id: number; code: string; name: string };
}

/**
 * Three designs of 200 / 100 / 50 mm3 in the Rings category. 22KY has 10 % wastage; 2HD is priced per
 * piece at 1.10 mm (100) and 1.20 mm (150); 1.30 mm has no price, which makes design C incomplete.
 *   A: 24 x 1.10 + 12 x 1.20          net 3.550  gold 3.905 g   -> 23430.00 + 4200.00 + 36 x 25 = 28530.00
 *   B: 10 x 1.10, volume 100          net 1.775  gold 1.9525 g  -> 11715.00 + 1000.00 + 10 x 25 = 12965.00
 *   C: 5 x 1.30 (no price)
 */
async function seed(page: Page): Promise<Seeded> {
  const tag = `${Date.now()}`.slice(-7);
  await setWastage(page, '22KY', '10');
  await setPrice(page, '1.10', '0.0070', '100');
  await setPrice(page, '1.20', '0.0095', '150');
  await setPrice(page, '1.30', '0.0100', null);
  const cats = (await api(page, 'GET', '/masters/categories')).body as any[];
  const rings = cats.find((c) => c.name === 'Rings');
  const make = async (label: string, volume: string, seats: object[]) => {
    const name = `Sheet ${tag} ${label}`;
    const d = await api(page, 'POST', '/designs', { name, category_id: rings.id, tag_ids: [] });
    expect(d.status).toBe(201);
    const v = await api(page, 'POST', `/designs/${d.body.id}/variations`, { volume_mm3: volume });
    expect(v.status).toBe(201);
    const s = await api(page, 'PUT', `/variations/${v.body.id}/seats`, { seats });
    expect(s.status).toBe(200);
    return { id: d.body.id as number, code: d.body.code as string, name };
  };
  const seat = (size: string, n: number) => ({ size_mm: size, stone_count: n, locked_stone_code: '' });
  return {
    tag,
    a: await make('A', '200', [seat('1.10', 24), seat('1.20', 12)]),
    b: await make('B', '100', [seat('1.10', 10)]),
    c: await make('C', '50', [seat('1.30', 5)]),
  };
}

async function openCostDialog(page: Page, s: Seeded, pick: (keyof Seeded)[]) {
  await page.goto('/');
  await page.getByLabel('Search code or name').fill(`Sheet ${s.tag}`);
  for (const k of pick) {
    const d = s[k] as Seeded['a'];
    await page.getByRole('checkbox', { name: `Select ${d.code}` }).check();
  }
  await expect(page.getByText(`${pick.length} selected`)).toBeVisible();
  await page.getByRole('button', { name: 'Cost selected' }).click();
  return page.getByRole('dialog');
}

async function fillCosting(page: Page, dialog: ReturnType<Page['getByRole']>, gold = '6000') {
  await dialog.getByLabel('Metal').click();
  await page.getByRole('option', { name: '22KY' }).click();
  await dialog.getByLabel('Stone code').click();
  await page.getByRole('option', { name: '2HD' }).click();
  await dialog.getByLabel('Gold rate / g').fill(gold);
  await dialog.getByLabel('Setting rate / pc').fill('25');
  await dialog.getByRole('button', { name: 'Preview' }).click();
}

async function downloaded(page: Page, click: () => Promise<void>): Promise<string> {
  const [dl] = await Promise.all([page.waitForEvent('download'), click()]);
  return readFileSync(await dl.path(), 'utf8');
}

test.describe.configure({ mode: 'serial' });

test('select two designs, cost them together, save the costing and open it later', async ({
  page,
}) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const s = await seed(page);

  await page.goto('/');
  await page.getByLabel('Search code or name').fill(`Sheet ${s.tag}`);
  await expect(page.getByRole('checkbox', { name: `Select ${s.c.code}` })).toBeVisible();
  // "Select all on this page" ticks every card; unticking it clears them again.
  await page.getByRole('checkbox', { name: 'Select all on this page' }).check();
  await expect(page.getByText('3 selected')).toBeVisible();
  await page.getByRole('checkbox', { name: 'Select all on this page' }).uncheck();
  await expect(page.getByText(/\d+ selected/)).toHaveCount(0);

  const dialog = await openCostDialog(page, s, ['a', 'b']);
  await fillCosting(page, dialog);
  const table = dialog.getByRole('table', { name: 'Costing preview' });
  await expect(table.getByRole('row', { name: new RegExp(s.a.code) })).toContainText('28530.00');
  await expect(table.getByRole('row', { name: new RegExp(s.b.code) })).toContainText('12965.00');
  await expect(table.getByRole('row', { name: /^Total/ })).toContainText('41495.00');
  await expect(table.getByRole('row', { name: new RegExp(s.a.code) })).toContainText('3.550');
  await expect(table.getByRole('row', { name: new RegExp(s.a.code) })).toContainText(s.a.name);

  const csv = await downloaded(page, () => dialog.getByRole('button', { name: 'Download CSV' }).click());
  const lines = csv.trim().split('\n');
  expect(lines).toHaveLength(4);
  expect(lines[0]).toMatch(/^variation,metal,stone_code,net_g,wastage_pct,/);
  expect(lines[1]).toContain(`${s.a.code}-V1,22KY,2HD,3.550,10,3.905,6000,23430.00,`);
  expect(lines[1]).toMatch(/,28530\.00$/);
  expect(lines[3]).toMatch(/^TOTAL,.*,41495\.00$/);

  // Offered after the download: keep it as a costing sheet.
  await expect(dialog.getByText('Downloaded.')).toBeVisible();
  await dialog.getByLabel('Costing sheet name').fill(`Autumn ${s.tag}`);
  await dialog.getByRole('button', { name: 'Save as costing sheet' }).click();
  await page.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(page).toHaveURL(/\/costing-sheets\/\d+$/);
  await expect(page.getByRole('heading', { name: `Autumn ${s.tag}` })).toBeVisible();
  await expect(page.getByText('41495.00').first()).toBeVisible();
  const sheetUrl = page.url();

  // The sheet's CSV is exactly the one downloaded then.
  expect(await downloaded(page, () => page.getByRole('button', { name: 'Download CSV' }).click())).toBe(csv);

  // Master data and the gold rate move on; the saved sheet and its CSV do not.
  await setWastage(page, '22KY', '20');
  await setPrice(page, '1.10', '0.0070', '500');
  await page.goto(sheetUrl);
  await expect(page.getByText('41495.00').first()).toBeVisible();
  await expect(page.getByRole('row', { name: new RegExp(s.a.code + '-V1') })).toContainText('23430.00');
  expect(await downloaded(page, () => page.getByRole('button', { name: 'Download CSV' }).click())).toBe(csv);
  await page.getByText('Rates used when this sheet was made').click();
  await expect(page.getByText('22KY: 10 %')).toBeVisible();

  // It is in the list.
  await page.getByRole('link', { name: 'Costing sheets' }).first().click();
  const row = page.getByRole('row', { name: new RegExp(`Autumn ${s.tag}`) });
  await expect(row).toContainText('22KY');
  await expect(row).toContainText('41495.00');

  // Re-cost: a new sheet with today's wastage / prices and a higher gold rate, and the difference.
  await row.getByRole('link').click();
  await page.getByRole('button', { name: 'Re-cost…' }).click();
  await page.getByRole('form', { name: 'Re-cost' }).getByLabel('Gold rate / g').fill('7000');
  await page.getByRole('form', { name: 'Re-cost' }).getByRole('button', { name: 'Re-cost' }).click();
  await expect(page.getByRole('heading', { name: `Autumn ${s.tag} (re-cost)` })).toBeVisible();
  await expect(page.getByRole('heading', { name: /Difference from/ })).toBeVisible();
  // wastage 20 % on 3.550 g at 7000: 4.26 g -> 29820.00 gold, stone 24 x 500 + 12 x 150 = 13800.00, 900.00
  await expect(page.getByRole('row', { name: new RegExp(s.a.code + '-V1') }).last()).toContainText('44520.00');
  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('heading', { name: 'Costing sheets' })).toBeVisible();
  await expect(page.getByRole('row', { name: new RegExp(`Autumn ${s.tag} \\(re-cost\\)`) })).toHaveCount(0);
  await expect(page.getByRole('row', { name: new RegExp(`Autumn ${s.tag}`) })).toBeVisible();

  // The audit log recorded who saved, downloaded and deleted.
  const audit = (await api(page, 'GET', '/audit?action=costing_sheet.&limit=50')).body as any[];
  const actions = audit.map((a) => a.action);
  for (const want of ['costing_sheet.create', 'costing_sheet.download', 'costing_sheet.recost', 'costing_sheet.delete']) {
    expect(actions).toContain(want);
  }

  // Put master data back for the tests that follow.
  await setWastage(page, '22KY', '10');
  await setPrice(page, '1.10', '0.0070', '100');
});

test('missing data is listed, never costed as zero, unless the user chooses to skip it', async ({
  page,
}) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const s = await seed(page);
  const dialog = await openCostDialog(page, s, ['a', 'c']);
  await fillCosting(page, dialog);

  await expect(dialog.getByRole('alert')).toContainText('Data is missing for 1 variation');
  await expect(dialog.getByRole('alert')).toContainText(`${s.c.code}-V1`);
  await expect(dialog.getByRole('alert')).toContainText('price not set for (1.30 mm, 2HD)');
  // All-or-nothing: nothing can be downloaded or saved until the user opts in.
  await expect(dialog.getByRole('button', { name: 'Download CSV' })).toBeDisabled();

  await dialog.getByRole('switch', { name: /Skip variations/ }).click();
  const csv = await downloaded(page, () => dialog.getByRole('button', { name: 'Download CSV' }).click());
  expect(csv).toContain(`${s.a.code}-V1,22KY`);
  expect(csv).toContain('NOT COSTED');
  expect(csv).toContain(`${s.c.code},${s.c.code}-V1,`);
  expect(csv.split('NOT COSTED')[0]).not.toContain(`${s.c.code}-V1`);

  await dialog.getByLabel('Costing sheet name').fill(`Partial ${s.tag}`);
  await dialog.getByRole('button', { name: 'Save as costing sheet' }).click();
  await page.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(page.getByText('1 variation not costed')).toBeVisible();
  await expect(page.getByText('price not set for (1.30 mm, 2HD)')).toBeVisible();
  // Clean up the sheet.
  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('heading', { name: 'Costing sheets' })).toBeVisible();
});

test('a production user has no way to cost or open costing sheets', async ({ page }) => {
  await signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  await expect(page.getByRole('link', { name: 'Costing sheets' })).toHaveCount(0);
  await expect(page.getByRole('checkbox')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Cost selected' })).toHaveCount(0);

  await page.goto('/costing-sheets');
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();

  // The server refuses even when the screens are bypassed.
  for (const [method, path, body] of [
    ['GET', '/costing-sheets', undefined],
    ['GET', '/costing-sheets/1', undefined],
    ['GET', '/costing-sheets/1/csv', undefined],
    ['DELETE', '/costing-sheets/1', undefined],
    ['POST', '/costing-sheets/1/recost', {}],
    ['POST', '/costing-sheets', { name: 'x', design_ids: [1], metal_code: '22KY', stone_code: '2HD', gold_rate_per_g: '1' }],
    ['POST', '/exports/costing-preview', { design_ids: [1], metal_code: '22KY', stone_code: '2HD', gold_rate_per_g: '1' }],
    ['POST', '/exports/costing-csv', { design_ids: [1], metal_code: '22KY', stone_code: '2HD', gold_rate_per_g: '1' }],
  ] as [string, string, unknown][]) {
    expect((await api(page, method, path, body)).status, `${method} ${path}`).toBe(403);
  }
});
