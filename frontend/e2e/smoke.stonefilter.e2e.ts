import { Page, expect, test } from '@playwright/test';
import { signIn } from './auth';

// Catalog: stone code, stone % range and sort, computed on the fly for the chosen metal. Runs after
// smoke.e2e.ts (category prefixes) and smoke.sheets.e2e.ts; uses its own stone sizes (1.40, 1.60).

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

/**
 * X: volume 200, 20 x 1.40 mm. 22KY net 3.550 g.
 *   1HD 0.0050 ct: 0.1000 ct, 0.020 g, gross 3.570, 0.56 %      2HD 0.0120 ct: 0.2400 ct, 0.048 g, gross 3.598, 1.33 %
 * Y: volume 200, 5 x 1.60 mm; only 1HD has a row for 1.60 (0.0200 ct): 1HD 0.020 g, gross 3.570, 0.56 %; 2HD no data.
 */
async function seed(page: Page): Promise<{ tag: string; x: string; y: string }> {
  const tag = `${Date.now()}`.slice(-7);
  const rows = [
    ['1.40', '1HD', '0.0050'],
    ['1.40', '2HD', '0.0120'],
    ['1.60', '1HD', '0.0200'],
  ].map(([size_mm, stone_code, carats_per_stone]) => ({
    size_mm,
    stone_code,
    sieve: '',
    carats_per_stone,
    rate_per_pc: null,
    rate_per_ct: null,
    rate_basis: '',
  }));
  const m = await api(page, 'PUT', '/masters/stone-matrix', { rows, baseline_code: '' });
  expect([200, 202]).toContain(m.status);
  const cats = (await api(page, 'GET', '/masters/categories')).body as any[];
  const rings = cats.find((c) => c.name === 'Rings');
  const make = async (label: string, size: string, n: number) => {
    const d = await api(page, 'POST', '/designs', { name: `Stone ${tag} ${label}`, category_id: rings.id, tag_ids: [] });
    expect(d.status).toBe(201);
    const v = await api(page, 'POST', `/designs/${d.body.id}/variations`, { volume_mm3: '200' });
    expect(v.status).toBe(201);
    const s = await api(page, 'PUT', `/variations/${v.body.id}/seats`, {
      seats: [{ size_mm: size, stone_count: n, locked_stone_code: '' }],
    });
    expect(s.status).toBe(200);
    return d.body.code as string;
  };
  return { tag, x: await make('X', '1.40', 20), y: await make('Y', '1.60', 5) };
}

test('catalog: stone code and stone % filters, figures on the cards, shareable link, back restores', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const s = await seed(page);
  const card = (code: string) => page.locator('mat-card', { hasText: code });

  // a shared link opens with every filter applied
  await page.goto(`/?q=${encodeURIComponent(`Stone ${s.tag}`)}&metal=22KY&stone=2HD`);
  await expect(card(s.x)).toContainText('Stone 2HD');
  await expect(card(s.x)).toContainText('0.048 g · 1.33 %');
  await expect(card(s.x)).toContainText('3.598 g');
  // no matrix row for 2HD at 1.60 mm: flagged, never shown as 0
  await expect(card(s.y)).toContainText('no data for 2HD');
  await expect(card(s.y)).not.toContainText('0.000 g');

  // switching the stone code recomputes the same design
  await page.getByLabel('Stone code').click();
  await page.getByRole('option', { name: /^Baseline/ }).click();
  await expect(card(s.x)).toContainText('0.020 g · 0.56 %');
  await expect(card(s.y)).toContainText('0.020 g · 0.56 %');
  await expect(page).toHaveURL(/metal=22KY/);
  await expect(page).not.toHaveURL(/stone=/);

  // a stone % range: 2HD 1 to 2 matches X only; Y (no data) is excluded, not treated as 0
  await page.getByLabel('Stone code').click();
  await page.getByRole('option', { name: '2HD' }).click();
  await page.getByLabel('Stone % min').fill('1');
  await page.getByLabel('Stone % max').fill('2');
  await expect(card(s.x)).toBeVisible();
  await expect(card(s.y)).toHaveCount(0);
  await expect(page).toHaveURL(/pct_min=1/);
  await expect(page).toHaveURL(/stone=2HD/);
  // inclusive boundary
  await page.getByLabel('Stone % min').fill('1.33');
  await page.getByLabel('Stone % max').fill('1.33');
  await expect(card(s.x)).toBeVisible();
  await page.getByLabel('Stone % min').fill('1.34');
  await expect(page.getByText('No designs match')).toBeVisible();
  await page.getByLabel('Stone % min').fill('abc');
  await expect(page.getByText('0 to 100, 2 decimals at most')).toBeVisible();
  await page.getByLabel('Stone % min').fill('1');
  await page.getByLabel('Stone % max').fill('2');
  await expect(card(s.x)).toBeVisible();

  // the selection (and the filters) survive going to a design and back
  await card(s.x).getByRole('checkbox').check();
  await card(s.x).getByRole('link', { name: 'Open', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/designs/${s.x}`));
  await page.goBack();
  await expect(page.getByLabel('Stone % min')).toHaveValue('1');
  await expect(page.getByLabel('Search code or name')).toHaveValue(`Stone ${s.tag}`);
  await expect(card(s.x).getByRole('checkbox')).toBeChecked();
  // changing a filter keeps the selection
  await page.getByLabel('Stone % max').fill('3');
  await expect(card(s.x).getByRole('checkbox')).toBeChecked();

  // sorting by stone %: with 2HD only X has a figure, so Y goes last
  await page.getByLabel('Stone % min').fill('');
  await page.getByLabel('Stone % max').fill('');
  await page.getByLabel('Sort by').click();
  await page.getByRole('option', { name: 'Stone %, high first' }).click();
  await expect(page).toHaveURL(/sort=stone_pct_desc/);
  const titles = page.locator('mat-card h2');
  await expect(titles.first()).toContainText(s.x);
  await expect(titles.nth(1)).toContainText(s.y);
});

test('catalog: the production role sees stone figures but no money', async ({ page }) => {
  await signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  await page.goto('/?q=Stone&metal=22KY&stone=2HD&pct_min=1.33&pct_max=1.33');
  await expect(page.locator('mat-card').first()).toContainText('1.33 %');
  await expect(page.locator('body')).not.toContainText(/wastage|rate per|price/i);
});
