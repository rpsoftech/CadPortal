import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { signIn } from './auth';
import { seedOrder } from './order-seed';
import { readZip } from './zip';

// The ZIP and the package of an order, through the real production stack: API level first, then the
// order-detail buttons. Runs after smoke.e2e.ts (category prefixes) like smoke.zip.e2e.ts.

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

test('an order downloads as a ZIP of the files it recorded, and as a package', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));

  const result = await page.evaluate(async () => {
    const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
    const auth = { Authorization: `Bearer ${s.token}` };
    const call = async (method: string, path: string, body?: unknown) => {
      const r = await fetch(`/api/v1${path}`, {
        method,
        headers: { ...auth, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const t = await r.text();
      return { status: r.status, body: t ? JSON.parse(t) : null };
    };
    const upload = async (vid: number, name: string, content: string) => {
      const fd = new FormData();
      fd.append('file', new Blob([content]), name);
      return (await fetch(`/api/v1/variations/${vid}/upload-cad`, { method: 'POST', headers: auth, body: fd })).status;
    };
    const b64 = async (url: string) => {
      const r = await fetch(url); // a browser opens the signed link without the token
      const buf = new Uint8Array(await r.arrayBuffer());
      let bin = '';
      for (const b of buf) bin += String.fromCharCode(b);
      return { status: r.status, type: r.headers.get('content-type'), disp: r.headers.get('content-disposition'), data: btoa(bin) };
    };

    const tag = `${Date.now()}`.slice(-7);
    const cats = (await call('GET', '/masters/categories')).body as { id: number; name: string }[];
    const rings = cats.find((c) => c.name === 'Rings')!;
    const d1 = (await call('POST', '/designs', { name: `OZip ${tag} A`, category_id: rings.id, tag_ids: [] })).body;
    const d2 = (await call('POST', '/designs', { name: `OZip ${tag} B`, category_id: rings.id, tag_ids: [] })).body;
    const v1 = (await call('POST', `/designs/${d1.id}/variations`, { volume_mm3: '200' })).body;
    const v2 = (await call('POST', `/designs/${d2.id}/variations`, { volume_mm3: '200' })).body;
    const stl1 = `solid o1-${tag}\nendsolid\n`.repeat(40);
    const stl2 = `solid o2-${tag}\nendsolid\n`.repeat(60);
    const uploads = [await upload(v1.id, 'one.stl', stl1), await upload(v2.id, 'two.stl', stl2)];
    const order = await call('POST', '/orders', {
      name: `ZIP ${tag}`, metal_code: '18KR', stone_code: '2HD', place: true,
      items: [{ variation_id: v1.id, quantity: 3 }, { variation_id: v2.id, quantity: 1 }],
    });
    // After the order is made, the designer replaces the first STL.
    const replaced = await upload(v1.id, 'one-new.stl', `solid changed ${tag}\nendsolid\n`.repeat(5));

    const link = await call('GET', `/orders/${order.body.id}/download/link?files=stl`);
    const zip = await b64(link.body.url);
    const plink = await call('GET', `/orders/${order.body.id}/package/link?files=stl,3dm`);
    const pkg = await b64(plink.body.url);
    const audit = await call('GET', '/audit?action=order.download');
    return { uploads, replaced, order: order.body, link: link.body, zip, plink: plink.body, pkg, audit: audit.body, stl1, stl2, v1: v1.code, v2: v2.code };
  });

  expect(result.uploads).toEqual([201, 201]);
  expect(result.replaced).toBe(201);
  expect(result.link).toMatchObject({ files: 2, missing: 0, changed: 1, package: false });
  expect(result.zip.status).toBe(200);
  expect(result.zip.type).toBe('application/zip');
  expect(result.zip.disp).toContain(`${result.order.number}-stl-`);

  const { files, comment } = readZip(Buffer.from(result.zip.data, 'base64'));
  expect([...files.keys()].sort()).toEqual([`${result.v1}.stl`, `${result.v2}.stl`, 'MANIFEST.csv'].sort());
  // The file the order recorded, not the replacement; the manifest and the ZIP comment say it changed.
  expect(files.get(`${result.v1}.stl`)!.toString()).toBe(result.stl1);
  expect(files.get(`${result.v2}.stl`)!.toString()).toBe(result.stl2);
  const manifest = files.get('MANIFEST.csv')!.toString();
  expect(manifest).toContain('current_file');
  expect(manifest).toMatch(new RegExp(`${result.v1}.*changed`));
  expect(manifest).toMatch(new RegExp(`${result.v2}.*same`));
  expect(comment).toContain('replaced or removed');

  expect(result.plink).toMatchObject({ package: true, files: 2, missing: 2 });
  const pkg = readZip(Buffer.from(result.pkg.data, 'base64'));
  const number = result.order.number as string;
  expect(pkg.files.get(`${number}-job-bags.pdf`)!.subarray(0, 4).toString()).toBe('%PDF');
  expect(pkg.files.get(`${number}-summary.csv`)!.toString()).toContain(`${number},`);
  expect(result.pkg.disp).toContain(`${number}-package-`);

  const downloads = (result.audit as { detail: string }[]).filter((a) => a.detail.includes(number));
  expect(downloads.length).toBeGreaterThanOrEqual(2);
  expect(downloads.some((a) => a.detail.includes('package=true'))).toBe(true);
});

test('the order screen downloads the STL ZIP and the package, after showing what they hold', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const o = await seedOrder(page, 'OZipUI');
  await page.goto(`/orders/${o.id}`);
  await expect(page.getByRole('table', { name: 'Order lines' })).toBeVisible();

  // No line has a 3dm file: the button is off and the page says why.
  await expect(page.getByRole('button', { name: 'Download 3dm' })).toBeDisabled();
  await expect(page.getByText('No line of this order has a 3dm file.')).toBeVisible();

  await page.getByRole('button', { name: 'Download STL' }).click();
  const dialog = page.getByRole('dialog', { name: 'Download STL files' });
  await expect(dialog.getByText(/^2 CAD files \(STL files\), .* from 2 lines\.$/)).toBeVisible();
  const [dl] = await Promise.all([
    page.waitForEvent('download'),
    dialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  expect(dl.suggestedFilename()).toContain(`${o.number}-stl-`);
  await expect(dialog.getByText(/The download has started/)).toBeVisible();
  const { files } = readZip(readFileSync(await dl.path()));
  expect([...files.keys()].sort()).toEqual([`${o.codes[0]}.stl`, `${o.codes[1]}.stl`, 'MANIFEST.csv'].sort());
  expect(files.get(`${o.codes[0]}.stl`)!.toString()).toBe(o.stl[0]);
  expect(files.get(`${o.codes[1]}.stl`)!.toString()).toBe(o.stl[1]);
  const manifest = files.get('MANIFEST.csv')!.toString();
  expect(manifest).toMatch(new RegExp(`${o.codes[0]}.*same`));
  expect(manifest).toMatch(new RegExp(`${o.codes[1]}.*same`));
  await dialog.getByRole('button', { name: 'Close' }).click();

  await page.getByRole('button', { name: 'Download package' }).click();
  const pdialog = page.getByRole('dialog', { name: 'Download order package' });
  await expect(pdialog.getByText(/^2 CAD files \(STL and 3dm files\), /)).toBeVisible();
  await expect(pdialog.getByText(/2 requested files are not on the order/)).toBeVisible();
  const [pdl] = await Promise.all([
    page.waitForEvent('download'),
    pdialog.getByRole('button', { name: 'Download ZIP' }).click(),
  ]);
  expect(pdl.suggestedFilename()).toContain(`${o.number}-package-`);
  const pkg = readZip(readFileSync(await pdl.path())).files;
  expect(pkg.get(`${o.number}-job-bags.pdf`)!.subarray(0, 4).toString()).toBe('%PDF');
  expect(pkg.get(`${o.number}-summary.csv`)!.toString()).toContain(`${o.number},`);
  expect(pkg.get(`${o.codes[0]}.stl`)!.toString()).toBe(o.stl[0]);
  expect(pkg.has('MANIFEST.csv')).toBe(true);
});

test("the order download dialog shows the server's words when it refuses, and production may download", async ({ page, browser }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  const o = await seedOrder(page, 'OZipErr');

  const ctx = await browser.newContext({ baseURL: process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080' });
  const prod = await ctx.newPage();
  await signIn(prod, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  await prod.goto(`/orders/${o.id}`);
  await expect(prod.getByRole('table', { name: 'Order lines' })).toBeVisible();
  await expect(prod.getByRole('button', { name: 'Cost this order' })).toHaveCount(0);
  await expect(prod.getByRole('button', { name: 'Download STL' })).toBeEnabled();

  await prod.route('**/orders/*/download/link*', (r) =>
    r.fulfill({
      status: 429,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'You already have a download running. Wait for it to finish.' }),
    }),
  );
  await prod.getByRole('button', { name: 'Download STL' }).click();
  await expect(prod.getByRole('alert').filter({ hasText: 'You already have a download running' })).toBeVisible();
  await expect(prod.getByRole('dialog').getByRole('button', { name: 'Download ZIP' })).toBeDisabled();
  await ctx.close();
});
