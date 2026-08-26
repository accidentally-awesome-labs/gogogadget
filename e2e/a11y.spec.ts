import { test, expect, type Browser, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { loginAs, type TestUser } from './helpers';
import { surfaces } from './generated/surfaces';

// Moderate is the floor. A moderate axe finding is a real barrier — an
// unlabelled control, insufficient contrast — not a style note, so it fails
// the run. Minor findings are attached to the report instead: visible to
// whoever reads it, without turning the gate into noise.
const FAILING_IMPACTS: Record<string, true> = { moderate: true, serious: true, critical: true };

async function scan(page: Page) {
  const results = await new AxeBuilder({ page }).analyze();
  const minor = results.violations.filter((v) => v.impact === 'minor');
  if (minor.length > 0) {
    test.info().annotations.push({
      type: 'axe-minor',
      description: minor.map((v) => `${v.id} (${v.nodes.length})`).join(', '),
    });
  }
  const violations = results.violations.filter((v) => FAILING_IMPACTS[v.impact ?? '']);
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

async function open(
  browser: Browser,
  persona: string,
  theme: 'light' | 'dark',
): Promise<{ page: Page; close: () => Promise<void> }> {
  const context = persona
    ? await loginAs(browser, persona as TestUser)
    : await browser.newContext();
  const page = await context.newPage();
  if (theme === 'dark') {
    await page.addInitScript(() => localStorage.setItem('theme', 'dark'));
    await page.emulateMedia({ colorScheme: 'dark' });
  }
  return { page, close: () => context.close() };
}

// Pages that axe must cover but the visual matrix deliberately excludes: the
// job queue mutates between scrapes and media thumbnails are not stable
// pixels, so a scan is the only gate they get. Filtered against the matrix so
// that a surface declared later stops being scanned twice.
const covered = new Set(surfaces.map((s) => s.path));
const scanOnlyPages: Array<[string, string]> = [
  ['/admin/jobs', 'admin'],
  ['/admin/media', 'admin'],
].filter((entry): entry is [string, string] => !covered.has(entry[0]));

for (const theme of ['light', 'dark'] as const) {
  test.describe(`a11y (${theme})`, () => {
    // The generated matrix is the same surface list the visual suite compares,
    // so a component family or scenario cannot be pixel-checked while its
    // accessibility goes unscanned.
    for (const surface of surfaces) {
      test(`surface ${surface.id}`, async ({ browser }) => {
        const { page, close } = await open(browser, surface.persona, theme);
        try {
          await page.goto(surface.path);
          await expect(page.locator(surface.persona ? 'main' : 'body')).toBeVisible();
          await scan(page);
        } finally {
          await close();
        }
      });
    }

    for (const [path, persona] of scanOnlyPages) {
      test(`scan-only ${path}`, async ({ browser }) => {
        const { page, close } = await open(browser, persona, theme);
        try {
          await page.goto(path);
          await scan(page);
        } finally {
          await close();
        }
      });
    }
  });
}
