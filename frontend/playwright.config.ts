import { defineConfig, devices } from '@playwright/test';

// Browser smoke tests against a running stack (see scripts/e2e.sh). They live outside src/ and
// use the .e2e.ts suffix so `ng test` never picks them up.
export default defineConfig({
  testDir: 'e2e',
  testMatch: '**/*.e2e.ts',
  fullyParallel: false, // the tests share one database and build on each other
  workers: 1,
  retries: process.env['CI'] ? 1 : 0,
  reporter: process.env['CI'] ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: process.env['E2E_BASE_URL'] ?? 'http://127.0.0.1:18080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
