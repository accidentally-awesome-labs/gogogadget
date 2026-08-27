import { test, expect, type Browser, type Page } from '@playwright/test';
import { loginAs, type TestUser } from './helpers';
import { surfaces, type Surface, type Viewport } from './generated/surfaces';

// Visual baselines are font-rendering-sensitive: they exist ONLY inside the
// pinned Playwright Linux container (make visual / make visual-update). Plain
// `make e2e` skips this file — macOS screenshots diff by design.
test.skip(!process.env.E2E_VISUAL, 'visual specs run only inside make visual / make visual-update');

const shot = {
  maxDiffPixelRatio: 0.01,
  animations: 'disabled' as const,
  caret: 'hide' as const,
};

// Tablet is a portrait iPad and mobile is a narrow phone in portrait — the two
// compositions a responsive layout is actually judged on either side of the
// `md` breakpoint. Desktop deliberately has no entry: the config's default
// viewport is the desktop baseline, so a surface cannot drift between the
// visual suite and the rest of the e2e run.
const viewportSizes: Partial<Record<Viewport, { width: number; height: number }>> = {
  tablet: { width: 820, height: 1180 },
  mobile: { width: 390, height: 844 },
};

async function openSurface(
  browser: Browser,
  surface: Surface,
  theme: 'light' | 'dark',
  viewport: Viewport,
): Promise<{ page: Page; close: () => Promise<void> }> {
  // An anonymous surface gets a bare context; a persona surface gets the
  // generated session cookie, so the pixels are the ones that persona sees.
  const context = surface.persona
    ? await loginAs(browser, surface.persona as TestUser)
    : await browser.newContext();
  const page = await context.newPage();
  const size = viewportSizes[viewport];
  if (size) {
    await page.setViewportSize(size);
  }
  if (theme === 'dark') {
    // Both halves are load-bearing: the shell reads localStorage, and
    // prefers-color-scheme drives the parts that have no class hook.
    await page.addInitScript(() => localStorage.setItem('theme', 'dark'));
    await page.emulateMedia({ colorScheme: 'dark' });
  }
  return { page, close: () => context.close() };
}

for (const theme of ['light', 'dark'] as const) {
  test.describe(`visual (${theme})`, () => {
    for (const surface of surfaces) {
      for (const viewport of surface.viewports) {
        test(`${surface.id} ${viewport}`, async ({ browser }) => {
          const { page, close } = await openSurface(browser, surface, theme, viewport);
          try {
            await page.goto(surface.path);
            // A persona surface renders inside an app/admin shell, so `main`
            // proves the shell resolved and not just that a body exists.
            await expect(page.locator(surface.persona ? 'main' : 'body')).toBeVisible();
            await expect(page).toHaveScreenshot(`${surface.id}-${theme}-${viewport}.png`, {
              ...shot,
              fullPage: surface.fullPage,
              // Masks are per surface by declaration. A blanket mask would
              // hide a real regression everywhere to stabilise the few
              // surfaces that genuinely carry a nondeterministic value.
              mask: surface.masks.map((selector) => page.locator(selector)),
            });
          } finally {
            await close();
          }
        });
      }
    }
  });
}
