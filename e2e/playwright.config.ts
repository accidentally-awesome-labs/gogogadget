import { defineConfig, devices } from '@playwright/test';
import { databaseURLs } from './generated/database';

// Port 18080 is e2e-only — never the dev port 8080, so Playwright cannot
// attach to a stray dev server. The e2e database is disposable and reseeded
// by globalSetup on every run.
//
// Its address is derived, not written here: generated/database.ts is rendered
// by `ggg sync` from the selected database adapter and the test environment's
// effective host port (5432 shifted to 15432), which is the stack
// `ggg test e2e` brings up. It used to be a literal localhost:5432 — the
// DEVELOPMENT port — so the suite started the test stack and then drove
// whatever answered on the other one, which on a machine running its own
// Postgres there is not this project's database at all.
//
// E2E_DATABASE_URL still wins: that is how CI points the suite at its own
// service container.
const databaseURL = process.env.E2E_DATABASE_URL ?? databaseURLs.test;
if (!databaseURL) {
  throw new Error(
    "no e2e database address: this project's test environment publishes no local Postgres, so set E2E_DATABASE_URL",
  );
}

// The server builds absolute redirect and link targets from APP_URL, so the
// app has to agree with the origin Playwright drives. Left at its default
// (:8080) the local billing checkout redirects the browser off the suite's
// server entirely, and the confirmation step 400s against a foreign session.
const baseURL = process.env.E2E_BASE_URL ?? 'http://localhost:18080';

export { databaseURL, baseURL };

export default defineConfig({
  testDir: '.',
  fullyParallel: true,
  retries: process.env.CI ? 2 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    // Mobile is scoped by testMatch on purpose. An unscoped project re-runs
    // every spec at a phone viewport, and snapshot names are per project — so
    // the visual suite would look for baselines that were never recorded, and
    // the flow specs would assert desktop-only chrome. iPhone 12 is the same
    // device descriptor mobile.spec.ts already uses; browserName follows it
    // because the descriptor's own default is WebKit and isMobile emulation
    // only works in Chromium.
    {
      name: 'mobile',
      testMatch: /a11y-states\.spec\.ts$/,
      use: { ...devices['iPhone 12'], browserName: 'chromium' },
    },
  ],
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
      APP_URL: baseURL,
      DATABASE_URL: databaseURL,
      DEV_AUTH_BYPASS: 'true',
      CLERK_PORTAL_URL: 'https://accounts.example.test',
      // Blanked deliberately. The server auto-loads `.env` in development, so a
      // developer with a real Clerk dev key gets clerk-js booted into every
      // page — and csp.spec.ts asserts that a page loads no third-party
      // origin, so it fails on their machine and passes in CI, which has no
      // `.env`. A test environment that configures a third-party provider
      // cannot check "no third-party requests". DEV_AUTH_BYPASS above is what
      // supplies identity here, so nothing needs the keys.
      CLERK_PUBLISHABLE_KEY: '',
      CLERK_SECRET_KEY: '',
      TEST_NOW: '2026-01-15T00:00:00Z',
      // One IP drives the whole suite in parallel; the production default
      // (100/min) sheds e2e traffic as abuse.
      RATE_LIMIT_RPM: '100000',
    },
  },
  globalSetup: './global-setup.ts',
});
