import { readFileSync } from 'node:fs';
import { Page, expect, test } from '@playwright/test';
import { signIn } from './auth';
import { PDFDocument } from 'pdf-lib';

// Runs after smoke.e2e.ts (file order), which has already set up the master data and categories.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

async function pdfPages(path: string): Promise<number> {
  const bytes = readFileSync(path);
  expect(bytes.subarray(0, 5).toString('latin1')).toBe('%PDF-');
  return (await PDFDocument.load(bytes)).getPageCount();
}

test('all job bags of a design print into one PDF, one page each; selected ones only; ?v= picks the variation', async ({
  page,
}) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));

  // A design with two variations, made through the API.
  const made = await page.evaluate(async () => {
    const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
    const call = async (method: string, url: string, body?: unknown) => {
      const r = await fetch(`/api/v1${url}`, {
        method,
        headers: { Authorization: `Bearer ${s.token}`, 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      return r.json();
    };
    const cats = await call('GET', '/masters/categories');
    const rings = cats.find((c: { name: string }) => c.name === 'Rings');
    const d = await call('POST', '/designs', { name: 'Job bag test', category_id: rings.id });
    await call('POST', `/designs/${d.id}/variations`, { volume_mm3: '150' });
    await call('POST', `/designs/${d.id}/variations`, { volume_mm3: '250' });
    return { code: d.code as string };
  });

  await page.goto(`/designs/${made.code}`);
  await expect(page.getByRole('heading', { name: 'Several job bags in one PDF' })).toBeVisible();

  // Print all: two variations, two pages.
  let download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Print all job bags' }).click();
  let file = await download;
  expect(file.suggestedFilename()).toBe(`jobbags-${made.code}.pdf`);
  expect(await pdfPages((await file.path())!)).toBe(2);

  // Print selected: nothing ticked is disabled; tick one, get one page.
  const selected = page.getByRole('button', { name: /Print selected/ });
  await expect(selected).toBeDisabled();
  await page.getByRole('checkbox', { name: `${made.code}-V2` }).check();
  await expect(selected).toBeEnabled();
  download = page.waitForEvent('download');
  await selected.click();
  file = await download;
  expect(await pdfPages((await file.path())!)).toBe(1);

  // The link in the job-bag QR code opens the design with that variation selected.
  await page.goto(`/designs/${made.code}?v=${made.code}-V2`);
  await expect(page.getByRole('radio', { name: `${made.code}-V2`, checked: true })).toBeVisible();
  await page.goto(`/designs/${made.code}?v=NOPE`);
  await expect(page.getByRole('radio', { name: `${made.code}-V1`, checked: true })).toBeVisible();
});

test('the catalog selection bar prints the job bags of the ticked designs', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByLabel('Search code or name').fill('Job bag test');
  // wait for the filtered list: only the one design may be ticked
  const cards = page.getByRole('checkbox', { name: /^Select [A-Z]+\d+/ });
  await expect(cards).toHaveCount(1);
  await cards.check();
  await page.getByRole('button', { name: 'Print job bags' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('heading', { name: 'Print job bags for 1 design' })).toBeVisible();
  await dialog.getByLabel('Metal').click();
  await page.getByRole('option', { name: '18KR' }).click();
  await dialog.getByLabel('Stone code').click();
  await page.getByRole('option', { name: '2HD' }).click();
  const download = page.waitForEvent('download');
  await dialog.getByRole('button', { name: 'Print', exact: true }).click();
  const file = await download;
  expect(file.suggestedFilename()).toBe('jobbags.pdf');
  expect(await pdfPages((await file.path())!)).toBe(2);
  await expect(dialog.getByText('The PDF has been saved.')).toBeVisible();
});

test('the production role may print several job bags', async ({ page }) => {
  await signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  const status = await page.evaluate(async () => {
    const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
    const r = await fetch('/api/v1/designs?q=Job%20bag%20test', { headers: { Authorization: `Bearer ${s.token}` } });
    const designs = await r.json();
    const id = designs[0].id;
    const pdf = await fetch('/api/v1/exports/job-bags-pdf', {
      method: 'POST',
      headers: { Authorization: `Bearer ${s.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ design_ids: [id], metal_code: '18KR', stone_code: '2HD' }),
    });
    return [pdf.status, pdf.headers.get('content-type')];
  });
  expect(status).toEqual([200, 'application/pdf']);
});
