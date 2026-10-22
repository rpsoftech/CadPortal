import { deflateSync } from 'node:zlib';
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

test('stone weight is entered in grams and priced per piece or per carat', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/master-data?tab=matrix');

  // One row: 0.0014 g a stone, 100 per piece (the defaults: grams, per piece).
  await page.getByRole('button', { name: 'Add row' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Size (mm)').fill('1.10');
  await dialog.getByLabel('Stone code').click();
  await page.getByRole('option', { name: '2HD' }).click();
  await dialog.getByLabel('Weight per stone').fill('0.0014');
  await expect(dialog.getByText('= 0.0070 ct')).toBeVisible();
  await expect(dialog.getByRole('combobox', { name: 'Weight unit' })).toContainText('grams');
  await expect(dialog.getByRole('combobox', { name: 'Priced per' })).toContainText('per piece');
  await dialog.getByRole('textbox', { name: 'Price' }).fill('100');
  await dialog.getByRole('button', { name: 'Save' }).click();
  const row = page.getByRole('row').filter({ hasText: '1.10' }).filter({ hasText: '2HD' });
  await expect(row).toContainText('0.00140'); // grams lead
  await expect(row).toContainText('0.0070');
  await expect(row).toContainText('100.00 per pc');

  // A weight that would need rounding is refused with the reason, not rounded.
  await page.getByRole('button', { name: 'Add row' }).click();
  await dialog.getByLabel('Size (mm)').fill('1.20');
  await dialog.getByLabel('Stone code').click();
  await page.getByRole('option', { name: '2HD' }).click();
  await dialog.getByLabel('Weight per stone').fill('0.0000001');
  await expect(
    dialog.getByText('0.0000005 ct, which has more than 6 decimal places'),
  ).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Save' })).toBeEnabled(); // the form allows it; save explains
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog.getByRole('alert')).toContainText('more than 6 decimal places');
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  // An import in grams, with one row priced per carat and one per piece.
  await page.getByRole('button', { name: 'Import CSV / paste' }).click();
  await dialog
    .getByLabel('Matrix data')
    .fill(
      'size_mm,stone_code,grams_per_stone,rate_per_pc,rate_per_ct,rate_basis\n' +
        '1.20,2HD,0.0019,150,,pc\n' +
        '2.50,2HD,0.013,,50000,ct',
    );
  await dialog.getByRole('button', { name: /^Import 2 rows/ }).click();
  const perCarat = page.getByRole('row').filter({ hasText: '2.50' }).filter({ hasText: '2HD' });
  await expect(perCarat).toContainText('0.01300');
  await expect(perCarat).toContainText('0.0650');
  await expect(perCarat).toContainText('50000.00 per ct');
  await expect(
    page.getByRole('row').filter({ hasText: '1.20' }).filter({ hasText: '2HD' }),
  ).toContainText('150.00 per pc');
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

  // A removal is not the end: the file is kept and can be put back, byte for byte.
  await page.getByRole('link', { name: 'Removed' }).click();
  const row = page.getByRole('row').filter({ hasText: 'LR0001-V1 · bague été.3dm' });
  await expect(row).toContainText('Removed');
  await expect(row).toContainText('30 more days');
  await row.getByRole('button', { name: /^Restore/ }).click();
  await expect(page.getByText('Restored LR0001-V1 · bague été.3dm')).toBeVisible();
  await expect(page.getByText('No removed files.')).toBeVisible();
  await page.goto('/designs/LR0001');
  const [again] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Download', exact: true }).click(),
  ]);
  expect(readFileSync(await again.path()).equals(original)).toBe(true);

  // Now remove it for the tests that follow.
  await page.getByRole('link', { name: 'Edit seats & files' }).click();
  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Remove' }).click();
  await expect(page.getByText('No 3dm file stored yet')).toBeVisible();
});

test('a deleted design can be restored, with its code and variations', async ({ page }) => {
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/designs/LR0002');
  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Delete design' }).click();
  await expect(page.getByRole('heading', { name: 'Catalog' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'LR0002', exact: true })).toHaveCount(0);

  await page.getByRole('link', { name: 'Removed' }).click();
  const row = page.getByRole('row').filter({ hasText: 'LR0002' });
  await expect(row).toContainText('By hand');
  await row.getByRole('button', { name: 'Restore design LR0002' }).click();
  await expect(page.getByText('Restored LR0002')).toBeVisible();
  await expect(page.getByText('No deleted designs.')).toBeVisible();
  await page.goto('/designs/LR0002');
  await expect(page.getByRole('heading', { name: 'LR0002' })).toBeVisible();
});

// A real PNG of any size, made without a library: each row is a filter byte plus RGB pixels.
function png(w: number, h: number): Buffer {
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const o = y * (w * 3 + 1) + 1 + x * 3;
      raw[o] = (x * 255) / w;
      raw[o + 1] = (y * 255) / h;
      raw[o + 2] = 120;
    }
  }
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (b: Buffer) => {
    let c = 0xffffffff;
    for (const x of b) c = crcTable[(c ^ x) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type: string, data: Buffer) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(body));
    return Buffer.concat([len, body, sum]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // RGB
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

// Not a playable video, but it carries the MP4 signature the server checks.
const fakeMp4 = Buffer.concat([
  Buffer.from('\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2', 'binary'),
  Buffer.alloc(2048, 7),
]);

test('photos and video: upload, cover on the catalog card, per-variation, limit of five, delete', async ({
  page,
}) => {
  test.setTimeout(120_000);
  await signIn(page, env('E2E_ADMIN_EMAIL'), env('E2E_ADMIN_PASSWORD'));
  await page.goto('/designs/LR0001');
  const gallery = page.getByRole('region', { name: 'Photos and video: Whole design' });
  await expect(gallery.getByText('0 of 5')).toBeVisible();

  // Two photos and a video at once, sent one after another.
  await gallery.locator('input[type=file]').setInputFiles([
    { name: 'front.png', mimeType: 'image/png', buffer: png(900, 600) },
    { name: 'side view.png', mimeType: 'image/png', buffer: png(400, 800) },
    { name: 'turntable.mp4', mimeType: 'video/mp4', buffer: fakeMp4 },
  ]);
  await expect(page.getByText('Added 3 files')).toBeVisible({ timeout: 60_000 });
  await expect(gallery.getByText('3 of 5')).toBeVisible();
  await expect(gallery.getByRole('button', { name: 'Open front.png' })).toBeVisible();
  await expect(gallery.getByRole('button', { name: 'Open turntable.mp4' })).toBeVisible();
  // The thumbnails really load (signed link, content-security-policy), not just exist.
  const thumb = gallery.getByRole('img', { name: 'front.png' });
  await expect(thumb).toBeVisible();
  await expect
    .poll(() => thumb.evaluate((i: HTMLImageElement) => i.naturalWidth))
    .toBeGreaterThan(0);

  // Choose the cover; it shows on the catalog card.
  await gallery
    .locator('li', { hasText: 'side view.png' })
    .getByRole('button', { name: 'Make cover' })
    .click();
  await expect(gallery.getByText('Cover', { exact: true })).toBeVisible();
  await page.goto('/');
  const cover = page.getByRole('img', { name: 'LR0001 cover photo' });
  await expect(cover).toBeVisible();
  await expect
    .poll(() => cover.evaluate((i: HTMLImageElement) => i.naturalWidth))
    .toBeGreaterThan(0);
  // The catalog thumbnail is the portrait photo that was chosen (400 x 800 shrinks to 240 x 480).
  expect(await cover.evaluate((i: HTMLImageElement) => i.naturalHeight > i.naturalWidth)).toBe(
    true,
  );

  // A photo opens large.
  await page.goto('/designs/LR0001');
  await gallery.getByRole('button', { name: 'Open front.png' }).click();
  await expect(page.getByRole('dialog').getByRole('img', { name: 'front.png' })).toBeVisible();
  await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click();

  // The same design's variation has its own five.
  const variation = page.getByRole('region', { name: 'Photos and video: LR0001-V1' });
  await variation.locator('input[type=file]').setInputFiles({
    name: 'detail.png',
    mimeType: 'image/png',
    buffer: png(300, 300),
  });
  await expect(variation.getByText('1 of 5')).toBeVisible({ timeout: 30_000 });
  await expect(variation.getByRole('button', { name: 'Make cover' })).toHaveCount(0);

  // Five fit on the design: three more are offered, two are accepted and one is told there is no room.
  await gallery.locator('input[type=file]').setInputFiles([
    { name: 'a.png', mimeType: 'image/png', buffer: png(50, 50) },
    { name: 'b.png', mimeType: 'image/png', buffer: png(50, 50) },
    { name: 'c.png', mimeType: 'image/png', buffer: png(50, 50) },
  ]);
  await expect(gallery.getByText('5 of 5')).toBeVisible({ timeout: 30_000 });
  await expect(gallery.getByText('c.png: no room left')).toBeVisible();
  await expect(gallery.getByRole('button', { name: 'Add photo or video' })).toBeDisabled();

  // A file of the wrong type is refused before anything is sent.
  await variation.locator('input[type=file]').setInputFiles({
    name: 'notes.pdf',
    mimeType: 'application/pdf',
    buffer: Buffer.from('%PDF-1.7'),
  });
  await expect(
    variation.getByText('use JPEG, PNG or WebP photos, or MP4, MOV or WebM video'),
  ).toBeVisible();

  // Delete one: room again.
  page.once('dialog', (d) => void d.accept());
  await gallery.locator('li', { hasText: 'a.png' }).getByRole('button', { name: 'Delete' }).click();
  await expect(gallery.getByText('4 of 5')).toBeVisible();
  await expect(gallery.getByRole('button', { name: 'Add photo or video' })).toBeEnabled();
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
  await expect(page.getByRole('link', { name: 'Removed' })).toHaveCount(0);

  // Production sees the photos but has no way to change them, and the server agrees.
  await expect(page.getByRole('img', { name: 'LR0001 cover photo' })).toBeVisible();
  await page.goto('/designs/LR0001');
  const gallery = page.getByRole('region', { name: 'Photos and video: Whole design' });
  await expect(gallery.getByRole('img', { name: 'front.png' })).toBeVisible();
  await expect(gallery.getByRole('button', { name: 'Add photo or video' })).toHaveCount(0);
  await expect(gallery.getByRole('button', { name: 'Delete' })).toHaveCount(0);
  await expect(gallery.getByRole('button', { name: 'Make cover' })).toHaveCount(0);
  const denied = await page.evaluate(async () => {
    const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
    const form = new FormData();
    form.append(
      'file',
      new Blob([new Uint8Array([0xff, 0xd8, 0xff, 0xe0])], { type: 'image/jpeg' }),
      'x.jpg',
    );
    const r = await fetch('/api/v1/designs/1/media', {
      method: 'POST',
      headers: { Authorization: `Bearer ${s.token}` },
      body: form,
    });
    return r.status;
  });
  expect(denied).toBe(403);

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
