import { defineConfig, devices } from '@playwright/test';

// Browser smoke tests against a running stack (see scripts/e2e.sh). They live outside src/ and
// use the .e2e.ts suffix so `ng test` never picks them up.
//
// Two projects:
//   main  the tests that create and change data. They share one database and build on each other,
//         so they run in file order on one worker.
//   a11y  the accessibility scans (smoke.zz-a11y.e2e.ts). They only read what `main` created and
//         do not depend on each other, so they run in parallel. scripts/e2e.sh runs them after
//         `main` with --workers=4; here `dependencies` keeps a plain `playwright test` correct.
const A11Y = '**/smoke.zz-a11y.e2e.ts';

export default defineConfig({
  testDir: 'e2e',
  testMatch: '**/*.e2e.ts',
  globalSetup: './e2e/global-setup.ts', // signs in once per E2E user (see e2e/auth.ts)
  fullyParallel: false,
  workers: 1,
  retries: process.env['CI'] ? 1 : 0,
  reporter: process.env['CI'] ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'main',
      testIgnore: A11Y,
      outputDir: 'test-results/main',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'a11y',
      testMatch: A11Y,
      dependencies: ['main'],
      fullyParallel: true,
      outputDir: 'test-results/a11y',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
