import { readFileSync } from 'node:fs';
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
// No codes: they are assigned from the category prefixes (LR0001, LE0001).
const IMPORT = [
  'code,name,category,tags,volume_mm3,ref_weight_g,ref_metal',
  ',Smoke ring,Rings,,200,,',
  ',Smoke stud,Earrings,,,3.116,18KY',
].join('\n');

test.describe.configure({ mode: 'serial' });

test('an empty catalog loads, and the admin is told setup is not finished', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await expect(page.getByText('No designs match')).toBeVisible();
  await expect(page.getByText('Setup is not finished')).toBeVisible();
});

test('no design can be created before its category has a prefix', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'New design' }).click();
  await page.getByRole('dialog').getByLabel('Category').click();
  await expect(page.getByRole('option', { name: /Rings.*no code prefix yet/ })).toBeDisabled();

  await page.keyboard.press('Escape'); // closes the option list
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();

  // The same through an import: the server says what to do.
  await page.getByRole('button', { name: 'Import' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Design data').fill(',Too early,Rings,,200,,');
  await dialog.getByRole('button', { name: 'Check' }).click();
  await expect(dialog.getByText('has no code prefix; set one in Master data first')).toBeVisible();
});

test('master data is entered in the app and the checklist follows', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('link', { name: 'Open the checklist' }).click();
  const checklist = page.locator('.checklist');
  await expect(checklist).toContainText('Wastage % is missing');
  await expect(checklist).toContainText('Category code prefixes are missing');
  await expect(checklist).toContainText('The stone matrix is empty');

  // Prefixes are typed straight into the table and saved when the field is left.
  await page
    .getByRole('link', { name: /Categories & tags/ })
    .first()
    .click();
  for (const [category, prefix] of [
    ['Rings', 'lr'],
    ['Earrings', 'le'],
    ['Pendants', 'lp'],
  ]) {
    const field = page.getByLabel(`Code prefix for ${category}`);
    await field.fill(prefix);
    await field.press('Enter');
    await expect(
      page.getByText(`${category} will be numbered ${prefix.toUpperCase()}0001`),
    ).toBeVisible();
  }
  await expect(page.getByRole('row', { name: /^Rings/ })).toContainText('LR0001');

  // A bad or duplicate prefix is refused and the old value comes back.
  const necklaces = page.getByLabel('Code prefix for Necklaces');
  await necklaces.fill('L1');
  await necklaces.press('Enter');
  await expect(page.getByText('A code prefix is 1 to 6 letters')).toBeVisible();
  await expect(necklaces).toHaveValue('');
  await necklaces.fill('LR');
  await necklaces.press('Enter');
  await expect(
    page.getByText('another category already uses that name or code prefix'),
  ).toBeVisible();

  // Wastage and the default alloy, in the alloy dialog.
  await page.getByRole('tab', { name: 'Alloys' }).click();
  await page.getByRole('row', { name: /^22KY/ }).getByRole('button', { name: 'Edit' }).click();
  const alloy = page.getByRole('dialog');
  await alloy.getByLabel('Wastage %').fill('10');
  await alloy.getByRole('switch', { name: /Default input/ }).click();
  await alloy.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Saved 22KY')).toBeVisible();
  await expect(checklist).toContainText('18KR, 18KY'); // 22KY is no longer listed
  await expect(checklist).not.toContainText('No default input alloy');
  // Only the categories still without a prefix are listed.
  await expect(checklist).toContainText('Bangles, Bracelets, Necklaces, Pendant Sets');
  await expect(checklist).not.toContainText('Rings');

  // Everything entered can be kept outside the database: one zip of CSV files.
  const [zip] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Export all (ZIP)' }).click(),
  ]);
  expect(zip.suggestedFilename()).toMatch(/^master-data-\d{4}-\d{2}-\d{2}\.zip$/);
  const bytes = readFileSync(await zip.path());
  expect(bytes.subarray(0, 2).toString()).toBe('PK');
  for (const name of ['alloys.csv', 'stone_matrix.csv', 'categories.csv', 'README.txt']) {
    expect(bytes.includes(Buffer.from(name))).toBe(true);
  }
});

test('importing designs checks first, then numbers them automatically', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'Import' }).click();
  const dialog = page.getByRole('dialog');

  // A bad file lists every problem and cannot be imported.
  await dialog
    .getByLabel('Design data')
    .fill(',Bad,Spaceships,,abc,,\nLE0099,Wrong prefix,Rings,,5,,');
  await dialog.getByRole('button', { name: 'Check' }).click();
  await expect(dialog.getByText('unknown category "Spaceships"')).toBeVisible();
  await expect(dialog.getByText('is not a number')).toBeVisible();
  await expect(dialog.getByText('must be LR followed by digits')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Import' })).toBeDisabled();

  await dialog.getByLabel('Design data').fill(IMPORT);
  await dialog.getByRole('button', { name: 'Check' }).click();
  await expect(dialog.getByText('2 designs and 2 variations will be created')).toBeVisible();
  await dialog.getByRole('button', { name: 'Import' }).click();

  await expect(page.getByText('Imported 2 designs (LR0001, LE0001)')).toBeVisible();
  await expect(page.getByText('LR0001').first()).toBeVisible();
  await expect(page.getByText('LE0001').first()).toBeVisible();
});

test('a manually created design takes the next number and has no code field', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.getByRole('button', { name: 'New design' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel('Design code')).toHaveCount(0);
  await dialog.getByLabel('Name').fill('By hand');
  await dialog.getByLabel('Category').click();
  await page.getByRole('option', { name: 'Rings', exact: true }).click();
  await expect(dialog.getByText('LR0002')).toBeVisible();
  await dialog.getByRole('button', { name: 'Create' }).click();
  await expect(page).toHaveURL(/\/designs\/LR0002$/);
});

test('the weight matrix matches the spec fixtures', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/designs/LR0001');
  const row = (metal: string) => page.getByRole('row').filter({ hasText: metal }).first();
  await expect(row('22KY')).toContainText('Net 3.550');
  await expect(row('18KY')).toContainText('Net 3.116');
  await expect(row('18KR')).toContainText('Net 3.050');

  // A reference weight of 3.116 g in 18KY scales to 3.050 g in 18KR.
  await page.goto('/designs/LE0001');
  await expect(row('18KR')).toContainText('Net 3.050');
});

test('a CAD file keeps its bytes and its accented name through upload, download and removal', async ({
  page,
}) => {
  test.setTimeout(180_000); // a real bucket over the internet is slower than local disk
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/designs/LR0001');
  await page.getByRole('link', { name: 'Edit seats & files' }).click();

  // 6 MiB of non-repeating bytes: large enough for a multipart upload to R2, and gzip cannot
  // shrink it, so a truncated or doubled stream would show.
  const name = 'bague été.3dm';
  const original = Buffer.alloc(6 << 20);
  for (let i = 0; i < original.length; i += 4)
    original.writeUInt32LE(Math.imul(i + 1, 2654435761) >>> 0, i);
  await page.locator('input[type=file][accept=".3dm"]').setInputFiles({
    name,
    mimeType: 'application/octet-stream',
    buffer: original,
  });
  await expect(page.getByText(`Stored: ${name}`)).toBeVisible({ timeout: 120_000 });

  // Download it from the design page, as a user would.
  await page.goto('/designs/LR0001');
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Download', exact: true }).click(),
  ]);
  expect(download.suggestedFilename()).toBe(name);
  const path = await download.path();

  expect(readFileSync(path).equals(original)).toBe(true);

  await page.goto('/designs/LR0001');
  await page.getByRole('link', { name: 'Edit seats & files' }).click();
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
  await expect(page.getByText('LR0001').first()).toBeVisible();
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
