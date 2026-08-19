import { defineConfig } from '@playwright/test';

// Port 18080 is e2e-only — never the dev port 8080, so Playwright cannot
// attach to a stray dev server. The e2e database is disposable and reseeded
// by globalSetup on every run. E2E_DATABASE_URL overrides for machines where
// 5432 is taken (CI uses the default).
const databaseURL =
  process.env.E2E_DATABASE_URL ??
  'postgres://postgres:postgres@localhost:5432/gogogadget_e2e?sslmode=disable';

export { databaseURL };

export default defineConfig({
  testDir: '.',
  fullyParallel: true,
  retries: process.env.CI ? 2 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:18080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
  // E2E_NO_WEBSERVER: the visual harness runs the server on the host and
  // Playwright inside the pinned container, which has no Go toolchain.
  webServer: process.env.E2E_NO_WEBSERVER
    ? undefined
    : {
    command: 'go run ./cmd/server',
    cwd: '..',
    url: 'http://localhost:18080/healthz',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    env: {
      APP_ENV: 'test',
      PORT: '18080',
      DATABASE_URL: databaseURL,
      DEV_AUTH_BYPASS: 'true',
      CLERK_PORTAL_URL: 'https://accounts.example.test',
      TEST_NOW: '2026-01-15T00:00:00Z',
      // One IP drives the whole suite in parallel; the production default
      // (100/min) sheds e2e traffic as abuse.
      RATE_LIMIT_RPM: '100000',
    },
  },
  globalSetup: './global-setup.ts',
});
