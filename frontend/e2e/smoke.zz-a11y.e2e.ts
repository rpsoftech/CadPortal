import { mkdirSync, writeFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { Browser, BrowserContext, Page, expect, test } from '@playwright/test';
import { admin } from './auth';

// Accessibility (WCAG 2.2 AA) over every screen, dialog and menu, in light and dark, at desktop and
// phone width. Runs after smoke.e2e.ts and smoke.mobile.e2e.ts (file name order) because it needs the
// designs those create. Fails on any serious or critical axe violation; the rest (moderate/minor) is
// written to test-results/a11y-minor.json and listed in the test output.

const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} must be set (scripts/e2e.sh does this)`);
  return v;
};

const SCHEMES = ['light', 'dark'] as const;
const SIZES = [
  { name: 'desktop', width: 1280, height: 800 },
  { name: 'phone', width: 375, height: 812 },
] as const;

interface Finding {
  where: string;
  id: string;
  impact: string;
  help: string;
  nodes: string[];
}

class Scanner {
  readonly blocking: Finding[] = [];
  readonly minor: Finding[] = [];
  readonly notes: string[] = [];
  constructor(
    private readonly page: Page,
    private readonly tag: string,
  ) {}

  /** Lets Material finish its open/close animation before axe measures colours. */
  private async settle(): Promise<void> {
    await this.page.waitForLoadState('networkidle');
    await this.page.waitForTimeout(450);
  }

  async scan(label: string): Promise<void> {
    await this.settle();
    const where = `${this.tag} / ${label}`;
    const results = await new AxeBuilder({ page: this.page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
      .analyze();
    for (const v of results.violations) {
      const f: Finding = {
        where,
        id: v.id,
        impact: v.impact ?? 'minor',
        help: v.help,
        nodes: v.nodes.slice(0, 4).map((n) => `${n.target.join(' ')} :: ${(n.failureSummary ?? '').split('\n').slice(1, 3).join(' ').trim()}`),
      };
      (f.impact === 'serious' || f.impact === 'critical' ? this.blocking : this.minor).push(f);
    }
  }

  /** Opens something, scans it, and closes it again with Escape. */
  async overlay(label: string, open: () => Promise<unknown>): Promise<void> {
    try {
      await open();
    } catch (e) {
      this.notes.push(`${this.tag} / ${label}: could not open (${String(e).split('\n')[0]})`);
      return;
    }
    await this.scan(label);
    await this.page.keyboard.press('Escape');
    // A dialog with unsaved-changes guards may need a second press; wait for it to go.
    await this.page.waitForTimeout(250);
    if ((await this.page.locator('.cdk-overlay-pane').count()) > 0) {
      await this.page.keyboard.press('Escape');
      await this.page.waitForTimeout(250);
    }
  }

  async go(url: string, label: string): Promise<void> {
    await this.page.goto(url);
    await this.page.waitForLoadState('networkidle');
    await this.scan(label);
  }
}

const signIn = admin;

async function newPage(
  browser: Browser,
  scheme: 'light' | 'dark',
  size: { width: number; height: number },
): Promise<{ ctx: BrowserContext; page: Page }> {
  const phone = size.width < 500;
  const ctx = await browser.newContext({
    baseURL: process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080',
    colorScheme: scheme,
    viewport: { width: size.width, height: size.height },
    isMobile: phone,
    hasTouch: phone,
  });
  return { ctx, page: await ctx.newPage() };
}

for (const scheme of SCHEMES) {
  for (const size of SIZES) {
    test(`axe: every screen, ${scheme}, ${size.name}`, async ({ browser }) => {
      test.setTimeout(480_000);
      const { ctx, page } = await newPage(browser, scheme, size);
      page.setDefaultTimeout(15_000);
      const phone = size.name === 'phone';
      const s = new Scanner(page, `${scheme}/${size.name}`);

      // --- signed out ---
      await s.go('/login', 'login');
      await page.getByLabel('Email').fill('nobody@example.com');
      await page.getByLabel('Password').fill('wrong-password-123');
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByRole('alert').first().waitFor({ timeout: 8000 }).catch(() => undefined);
      await s.scan('login with an error');
      await s.go('/forgot-password', 'forgot password');
      await s.go('/reset-password?token=not-a-real-token', 'reset password');

      // --- signed in ---
      await signIn(page);
      await s.scan('catalog');
      // A design ticked: the selection bar with the CAD download buttons, and the download dialog.
      await page.getByRole('checkbox', { name: /^Select [A-Z]+\d+/ }).first().check();
      await s.scan('catalog: selection bar');
      await s.overlay('catalog: download STL dialog', () => page.getByRole('button', { name: 'Download STL' }).click());
      await page.getByRole('button', { name: 'Clear', exact: true }).click();
      const filters = page.getByRole('button', { name: /^Filters/ });
      if (phone) {
        await filters.click();
        await expect(page.getByLabel('Net min (g)')).toBeVisible();
        await s.scan('catalog: filters panel open');
      }
      await s.overlay('catalog: target purity list', async () => {
        await page.getByLabel('Target purity').click();
        await page.getByRole('option').first().waitFor();
      });
      if (phone) await filters.click();
      await s.overlay('catalog: new design dialog', () => page.getByRole('button', { name: 'New design' }).click());
      await s.overlay('catalog: import dialog', () => page.getByRole('button', { name: 'Import', exact: true }).click());
      await s.overlay('account menu', () => page.getByRole('button', { name: 'Account menu' }).click());
      await s.overlay('change password dialog', async () => {
        await page.getByRole('button', { name: 'Account menu' }).click();
        await page.getByRole('menuitem', { name: 'Change password' }).click();
        await page.getByRole('dialog').waitFor();
      });
      if (phone) {
        await s.overlay('navigation menu', () => page.getByRole('button', { name: 'Menu', exact: true }).click());
      }

      // catalog with a metal and stone code: the stone figures on the cards, and the stone filters
      await s.go('/?metal=22KY&stone=2HD&pct_min=0&sort=stone_pct_desc', 'catalog: stone figures');
      if (phone) {
        await filters.click();
        await expect(page.getByLabel('Stone % min')).toBeVisible();
        await s.scan('catalog: stone filters panel open');
        await filters.click();
      }
      await s.overlay('catalog: stone code list', async () => {
        if (phone) await filters.click();
        await page.getByLabel('Stone code').click();
        await page.getByRole('option').first().waitFor();
      });

      // design detail
      await s.go('/designs/LR0001', 'design detail');
      await s.overlay('design: metal list', async () => {
        await page.getByRole('combobox', { name: 'Metal' }).click();
        await page.getByRole('option').first().waitFor();
      });
      await s.overlay('design: edit dialog', () => page.getByRole('button', { name: 'Edit design' }).click());
      await s.overlay('design: add variation dialog', () => page.getByRole('button', { name: 'Add variation' }).click());
      const thumb = page.locator('app-media-gallery button.thumb, app-media-gallery .thumb button, app-media-gallery [role="listitem"] button').first();
      if (await thumb.count()) {
        await s.overlay('design: media viewer', async () => {
          await thumb.click();
          await page.getByRole('dialog').waitFor();
        });
      }

      // variation editor
      await page.getByRole('link', { name: 'Edit seats & files' }).click();
      await page.waitForURL(/\/edit$/);
      await s.scan('variation editor');

      // master data
      await s.go('/master-data', 'master data: alloys');
      await s.overlay('alloy dialog', () => page.getByRole('button', { name: 'Add alloy' }).click());
      await s.go('/master-data?tab=matrix', 'master data: stone matrix');
      await s.overlay('stone code dialog', () => page.getByRole('button', { name: 'Add stone code' }).click());
      await s.overlay('matrix row dialog', () => page.getByRole('button', { name: 'Add row' }).click());
      await s.overlay('matrix import dialog', () => page.getByRole('button', { name: /^Import CSV/ }).click());
      await s.go('/master-data?tab=categories', 'master data: categories & tags');
      await s.overlay('category dialog', () => page.getByRole('button', { name: 'Add category' }).click());
      await s.overlay('tag dialog', () => page.getByRole('button', { name: 'Add tag' }).click());

      // the rest
      await s.go('/removed', 'removed');
      await s.go('/audit', 'audit log');
      await s.go('/backups', 'backups');
      await s.go('/users', 'users');
      await s.overlay('new user dialog', () => page.getByRole('button', { name: 'New user' }).click());
      await s.overlay('edit user dialog', () => page.getByRole('button', { name: 'Edit' }).first().click());
      await s.overlay('set password dialog', () => page.getByRole('button', { name: 'Set password' }).first().click());

      await ctx.close();

      mkdirSync('test-results', { recursive: true });
      writeFileSync(
        `test-results/a11y-${scheme}-${size.name}.json`,
        JSON.stringify({ blocking: s.blocking, minor: s.minor, notes: s.notes }, null, 2),
      );
      const fmt = (f: Finding) => `[${f.impact}] ${f.where}: ${f.id} - ${f.help}\n      ${f.nodes.join('\n      ')}`;
      if (s.minor.length) console.log(`Minor/moderate findings (${scheme}/${size.name}):\n  ${s.minor.map(fmt).join('\n  ')}`);
      expect(s.notes, 'every dialog and menu could be opened').toEqual([]);
      expect(s.blocking.map(fmt), 'serious or critical axe violations').toEqual([]);
    });
  }
}

// ---- Things axe cannot see: keyboard, focus, motion, reflow --------------------------------------

test('keyboard: skip link, focus after navigation, dialog focus, visible focus ring', async ({ page }) => {
  await signIn(page);

  // The skip link is the first stop and moves focus to the page.
  await page.goto('/');
  await page.waitForLoadState('networkidle');
  await page.keyboard.press('Tab');
  const skip = page.getByRole('link', { name: 'Skip to content' });
  await expect(skip).toBeFocused();
  await expect(skip).toBeInViewport();
  const ring = await skip.evaluate((el) => getComputedStyle(el).outlineWidth);
  expect(parseFloat(ring)).toBeGreaterThanOrEqual(2);
  await page.keyboard.press('Enter');
  await expect(page.locator('main')).toBeFocused();

  // After a route change the page heading has focus and the title is announced.
  await page.getByRole('link', { name: 'Removed', exact: true }).first().click();
  await expect(page.getByRole('heading', { name: 'Removed', level: 1 })).toBeFocused();
  await expect(page.locator('[role="status"].sr-only').first()).toHaveText('Removed');
  await page.getByRole('link', { name: 'Users' }).click();
  await expect(page.getByRole('heading', { name: 'Users', level: 1 })).toBeFocused();

  // A dialog takes focus, keeps it inside, and gives it back to the button that opened it.
  await page.goto('/');
  const open = page.getByRole('button', { name: 'New design' });
  await open.focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect.poll(() => dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press('Tab');
    expect(await dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(open).toBeFocused();

  // A control that Material does not style gets a visible ring.
  await page.goto('/designs/LR0001');
  await page.waitForLoadState('networkidle');
  const back = page.getByRole('link', { name: '← Catalog' });
  await back.focus();
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Tab');
  const outline = await back.evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(outline).not.toBe('none');
});

test('keyboard: the media gallery, file zones and seat grid work without a mouse', async ({ page }) => {
  await signIn(page);
  await page.goto('/designs/LR0001');
  const tile = page.getByRole('button', { name: /^Open front\.png/ });
  await tile.focus();
  await page.keyboard.press('Enter');
  const viewer = page.getByRole('dialog');
  await expect(viewer).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(tile).toBeFocused();

  await page.getByRole('link', { name: 'Edit seats & files' }).click();
  await page.waitForURL(/\/edit$/);
  const zone = page.getByRole('button', { name: /Upload.*\.3dm/ });
  await zone.focus();
  await expect(zone).toBeFocused();
  const chooser = page.waitForEvent('filechooser');
  await page.keyboard.press('Enter');
  await chooser;

  await page.getByRole('button', { name: 'Add seat' }).focus();
  await page.keyboard.press('Enter');
  const size = page.getByLabel('Seat 1 size in mm');
  await size.focus();
  await page.keyboard.type('1.5');
  await page.keyboard.press('Tab');
  await page.keyboard.type('3');
  await expect(page.getByLabel('Seat 1 stone count')).toHaveValue('3');
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter'); // opens the lock list
  await expect(page.getByRole('option', { name: 'Flexible' })).toBeVisible();
  await page.keyboard.press('Escape');
});

test('reduced motion and 200% text keep everything usable', async ({ browser }) => {
  test.setTimeout(120_000);
  // 200 % zoom of a 1280 px window is a 640 px page with text twice as large; 320 px (400 %) is checked
  // at normal text size in smoke.mobile.e2e.ts.
  const c2 = await browser.newContext({
    baseURL: process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080',
    viewport: { width: 640, height: 640 },
    reducedMotion: 'reduce',
  });
  const p = await c2.newPage();
  p.setDefaultTimeout(15_000);
  await signIn(p);
  for (const url of ['/', '/designs/LR0001', '/users', '/audit', '/master-data?tab=matrix']) {
    await p.goto(url);
    await p.addStyleTag({ content: 'html { font-size: 200% !important; }' });
    await p.waitForLoadState('networkidle');
    const w = await p.evaluate(() => ({ s: document.documentElement.scrollWidth, c: document.documentElement.clientWidth }));
    expect(w.s, `${url} scrolls sideways at 640 px with 200% text`).toBeLessThanOrEqual(w.c);
  }
  const dur = await p.evaluate(() => getComputedStyle(document.body).transitionDuration);
  expect(dur === '0s' || parseFloat(dur) < 0.001).toBe(true);
  const spin = await p.locator('mat-toolbar').evaluate((el) => getComputedStyle(el).animationDuration);
  expect(parseFloat(spin)).toBeLessThan(0.001);
  await c2.close();
});
