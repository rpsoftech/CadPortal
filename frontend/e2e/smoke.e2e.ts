import { Page, expect, test } from '@playwright/test';

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

// Uses the spec fixtures: volume 200 mm3 is 3.550 g in 22KY, 3.116 g in 18KY, 3.050 g in 18KR.
const IMPORT = [
  'code,name,category,tags,volume_mm3,ref_weight_g,ref_metal',
  'E2E001,Smoke ring,Rings,,200,,',
  'E2E002,Smoke stud,Earrings,,,3.116,18KY',
].join('\n');

test.describe.configure({ mode: 'serial' });

test('an empty catalog loads and an admin can sign in', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await expect(page.getByText('No designs match')).toBeVisible();
});

test('importing designs checks first, then creates them', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'Import' }).click();
  const dialog = page.getByRole('dialog');

  // A bad file lists every problem and cannot be imported.
  await dialog.getByLabel('Design data').fill('E2E900,Bad,Spaceships,,abc,,');
  await dialog.getByRole('button', { name: 'Check' }).click();
  await expect(dialog.getByText('unknown category "Spaceships"')).toBeVisible();
  await expect(dialog.getByText('is not a number')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Import' })).toBeDisabled();

  await dialog.getByLabel('Design data').fill(IMPORT);
  await dialog.getByRole('button', { name: 'Check' }).click();
  await expect(dialog.getByText('2 designs and 2 variations will be created')).toBeVisible();
  await dialog.getByRole('button', { name: 'Import' }).click();

  await expect(page.getByText('Imported 2 designs')).toBeVisible();
  await expect(page.getByText('E2E001').first()).toBeVisible();
  await expect(page.getByText('E2E002').first()).toBeVisible();
});

test('the weight matrix matches the spec fixtures', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/designs/E2E001');
  const row = (metal: string) => page.getByRole('row').filter({ hasText: metal }).first();
  await expect(row('22KY')).toContainText('Net 3.550');
  await expect(row('18KY')).toContainText('Net 3.116');
  await expect(row('18KR')).toContainText('Net 3.050');

  // A reference weight of 3.116 g in 18KY scales to 3.050 g in 18KR.
  await page.goto('/designs/E2E002');
  await expect(row('18KR')).toContainText('Net 3.050');
});

test('a CAD file can be uploaded, then removed', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/designs/E2E001');
  await page.getByRole('link', { name: 'Edit seats & files' }).click();

  await page.locator('input[type=file][accept=".3dm"]').setInputFiles({
    name: 'smoke.3dm',
    mimeType: 'application/octet-stream',
    buffer: Buffer.from('not a real 3dm file, but bytes are bytes'),
  });
  await expect(page.getByText('Stored: smoke.3dm')).toBeVisible();

  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Remove' }).click();
  await expect(page.getByText('No 3dm file stored yet')).toBeVisible();
});

test('the audit log shows what was done', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('link', { name: 'Audit' }).click();
  for (const action of ['design.import', 'cad.upload', 'cad.remove']) {
    await expect(page.getByRole('cell', { name: action, exact: true }).first()).toBeVisible();
  }
});

test('a production user can read but not edit', async ({ page }) => {
  await signIn(page, env('E2E_PROD_EMAIL'), env('E2E_PROD_PASSWORD'));
  await expect(page.getByText('E2E001').first()).toBeVisible();
  await expect(page.getByRole('button', { name: 'Export CSV' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'New design' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Import' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Audit' })).toHaveCount(0);

  // The server refuses even when the UI is bypassed, and the route guard bounces the page.
  await page.goto('/audit');
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
  const status = await page.evaluate(async () => {
    const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
    const r = await fetch('/api/v1/audit', { headers: { Authorization: `Bearer ${s.token}` } });
    return r.status;
  });
  expect(status).toBe(403);
});
