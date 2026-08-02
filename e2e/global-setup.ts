import { execSync } from 'node:child_process';
import { databaseURL } from './playwright.config';

// Reseed the disposable e2e database before the suite (fixed fixtures).
// Skipped in container mode (E2E_NO_WEBSERVER): visual-update.sh seeds on the
// host — the pinned Playwright image has no Go toolchain.
export default async function globalSetup() {
  if (process.env.E2E_NO_WEBSERVER) return;
  execSync('go run ./cmd/seed -reset internal/db/testdata/seed_e2e.sql', {
    cwd: '..',
    stdio: 'inherit',
    env: { ...process.env, DATABASE_URL: databaseURL },
  });
}
