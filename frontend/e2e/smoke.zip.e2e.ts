import { readFileSync } from 'node:fs';
import { Page, expect, test } from '@playwright/test';
import { signIn } from './auth';
import { readZip } from './zip';

// One ZIP of the STL / 3dm files of the selected designs. Runs after smoke.e2e.ts (it needs the category
// prefixes that test enters) and makes its own designs and files.

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

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

async function upload(page: Page, variationId: number, name: string, content: string): Promise<void> {
  const status = await page.evaluate(
    async ({ variationId, name, content }) => {
      const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
      const fd = new FormData();
      fd.append('file', new Blob([content]), name);
      const r = await fetch(`/api/v1/variations/${variationId}/upload-cad`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${s.token}` },
        body: fd,
      });
      return r.status;
    },
    { variationId, name, content },
  );
  expect(status).toBe(201);
}

interface Seeded {
  tag: string;
  a: { id: number; code: string; stl: string; dm: string };
  b: { id: number; code: string; stl: string };
  /** Second variation of B: has no STL file. */
  bMissing: string;
}

/**
 * A has one variation with an STL and a 3dm file. B has two variations: V1 has an STL file, V2 has none,
 * so the STL ZIP of A and B holds two files and lists one as missing.
 */
async function seed(page: Page): Promise<Seeded> {
  const tag = `${Date.now()}`.slice(-7);
  const cats = (await api(page, 'GET', '/masters/categories')).body as any[];
  const rings = cats.find((c) => c.name === 'Rings');
  const design = async (label: string) => {
    const d = await api(page, 'POST', '/designs', { name: `Zip ${tag} ${label}`, category_id: rings.id, tag_ids: [] });
    expect(d.status).toBe(201);
    return d.body as { id: number; code: string };
  };
  const variation = async (designId: number) => {
    const v = await api(page, 'POST', `/designs/${designId}/variations`, { volume_mm3: '200' });
    expect(v.status).toBe(201);
    return v.body as { id: number; code: string };
  };
  const a = await design('A');
  const b = await design('B');
  const av = await variation(a.id);
  const bv1 = await variation(b.id);
  const bv2 = await variation(b.id);
  const stlA = `solid zip-${tag}-a\nendsolid\n`.repeat(50);
  const dmA = `3D Geometry File Format ${tag}`;
  const stlB = `solid zip-${tag}-b\nendsolid\n`.repeat(70);
  await upload(page, av.id, 'ring a.stl', stlA);
  await upload(page, av.id, 'ring a.3dm', dmA);
  await upload(page, bv1.id, 'ring b.stl', stlB);
  return {
    tag,
    a: { id: a.id, code: a.code, stl: stlA, dm: dmA },
    b: { id: b.id, code: b.code, stl: stlB },
    bMissing: bv2.code,
  };
}

function parseCsv(text: string): Record<string, string>[] {
  // The manifest quotes only when needed and the test values have no embedded newlines.
  const rows = text
    .trim()
    .split('\n')
    .map((l) => l.split(',').map((c) => c.replace(/^"|"$/g, '')));
  return rows.slice(1).map((r) => Object.fromEntries(rows[0].map((h, i) => [h, r[i]])));
}

async function select(page: Page, s: Seeded, codes: string[]): Promise<void> {
  await page.goto('/');
  await page.getByLabel('Search code or name').fill(`Zip ${s.tag}`);
  for (const c of codes) await page.getByRole('checkbox', { name: `Select ${c}` }).check();
  await expect(page.getByText(`${codes.length} selected`)).toBeVisible();
}

test.describe.configure({ mode: 'serial' });

test('select two designs and download one ZIP of their STL files', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const s = await seed(page);
  await select(page, s, [s.a.code, s.b.code]);

  await page.getByRole('button', { name: 'Download STL' }).click();
  const dialog = page.getByRole('dialog');
  // The estimate comes before anything is downloaded: 2 files, and the variation without one is called out.
  await expect(dialog.getByText(/^2 STL files, .* from 3 variations\.$/)).toBeVisible();
  await expect(dialog.getByText('1 variation has no STL file.')).toBeVisible();

  const [dl] = await Promise.all([
    page.waitForEvent('download'),
    dialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  expect(dl.suggestedFilename()).toMatch(/^cad-stl-\d{8}-\d{4}\.zip$/);
  await expect(dialog.getByText(/The download has started/)).toBeVisible();

  const { files, comment } = readZip(readFileSync(await dl.path()));
  expect([...files.keys()].sort()).toEqual([`${s.a.code}-V1.stl`, `${s.b.code}-V1.stl`, 'MANIFEST.csv'].sort());
  // The original bytes, not the gzip the vault stores.
  expect(files.get(`${s.a.code}-V1.stl`)!.toString()).toBe(s.a.stl);
  expect(files.get(`${s.b.code}-V1.stl`)!.toString()).toBe(s.b.stl);
  expect(comment).toContain('WARNING: 1 file(s)');

  const manifest = parseCsv(files.get('MANIFEST.csv')!.toString());
  expect(manifest).toHaveLength(3);
  const a = manifest.find((r) => r.variation === `${s.a.code}-V1`)!;
  expect(a).toMatchObject({ status: 'ok', original_name: 'ring a.stl', file_in_zip: `${s.a.code}-V1.stl`, kind: 'stl' });
  expect(a.expected_sha256).toMatch(/^[0-9a-f]{64}$/);
  expect(a.actual_sha256).toBe(a.expected_sha256);
  expect(a.bytes_written).toBe(String(Buffer.byteLength(s.a.stl)));
  const missing = manifest.find((r) => r.variation === s.bMissing)!;
  expect(missing).toMatchObject({ status: 'missing', file_in_zip: '' });
  expect(missing.detail).toContain('no stl file');

  // It was audited.
  const audit = await api(page, 'GET', '/audit?action=download.cad_zip');
  expect(audit.body[0].detail).toContain('files=2');
  expect(audit.body[0].detail).toContain('missing=1');
});

test('the 3dm download, and a selection with no such file', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const s = await seed(page);

  await select(page, s, [s.a.code]);
  await page.getByRole('button', { name: 'Download 3dm' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText(/^1 3dm file, .* from 1 variation\.$/)).toBeVisible();
  const [dl] = await Promise.all([
    page.waitForEvent('download'),
    dialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  expect(dl.suggestedFilename()).toMatch(/^cad-3dm-/);
  const { files } = readZip(readFileSync(await dl.path()));
  expect(files.get(`${s.a.code}-V1.3dm`)!.toString()).toBe(s.a.dm);
  await dialog.getByRole('button', { name: 'Close' }).click();

  // Design B has no 3dm file at all: nothing to download, and the dialog says so instead of making an empty ZIP.
  await page.getByRole('checkbox', { name: `Select ${s.a.code}` }).uncheck();
  await page.getByRole('checkbox', { name: `Select ${s.b.code}` }).check();
  await page.getByRole('button', { name: 'Download 3dm' }).click();
  await expect(page.getByRole('dialog').getByText(/^None of the 2 variations has a 3dm file\.$/)).toBeVisible();
  await expect(page.getByRole('dialog').getByRole('button', { name: 'Download ZIP' })).toBeDisabled();
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();

  // The same on a phone (375 px; the same session, to stay clear of the sign-in rate limit).
  await page.setViewportSize({ width: 375, height: 812 });
  await page.getByRole('checkbox', { name: `Select ${s.b.code}` }).uncheck();
  await page.getByRole('checkbox', { name: `Select ${s.a.code}` }).check();
  for (const name of ['Download STL', 'Download 3dm']) {
    const box = await page.getByRole('button', { name }).boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(375 + 1);
    expect(box!.height).toBeGreaterThanOrEqual(40);
  }
  await page.getByRole('button', { name: 'Download STL' }).click();
  const phoneDialog = page.getByRole('dialog');
  await expect(phoneDialog.getByText(/^1 STL file, /)).toBeVisible();
  const dbox = await phoneDialog.boundingBox();
  expect(dbox!.x + dbox!.width).toBeLessThanOrEqual(375 + 1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375);
  const [phoneDl] = await Promise.all([
    page.waitForEvent('download'),
    phoneDialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  expect(readZip(readFileSync(await phoneDl.path())).files.has(`${s.a.code}-V1.stl`)).toBe(true);
});

test('production can download CAD ZIPs but has no costing button', async ({ page, browser }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const s = await seed(page);

  const ctx = await browser.newContext({ baseURL: process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080' });
  const prod = await ctx.newPage();
  await signIn(prod, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  await select(prod, s, [s.a.code]);
  await expect(prod.getByRole('button', { name: 'Cost selected' })).toHaveCount(0);
  await prod.getByRole('button', { name: 'Download STL' }).click();
  const dialog = prod.getByRole('dialog');
  const [dl] = await Promise.all([
    prod.waitForEvent('download'),
    dialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  const { files } = readZip(readFileSync(await dl.path()));
  expect(files.get(`${s.a.code}-V1.stl`)!.toString()).toBe(s.a.stl);
  await ctx.close();
});
