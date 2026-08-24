import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Visual baselines are font-rendering-sensitive: they exist ONLY inside the
// pinned Playwright Linux container (make visual-update). Plain `make e2e`
// skips this file — macOS screenshots diff by design.
test.skip(!process.env.E2E_VISUAL, 'visual specs run only inside make visual-update');

const shot = {
  maxDiffPixelRatio: 0.01,
  animations: 'disabled' as const,
  caret: 'hide' as const,
};

const maskSelectors = ['[data-testid="relative-time"]', 'img[alt*="avatar" i]'];

const publicPages = [
  ['home', '/'],
  ['pricing', '/pricing'],
  ['blog', '/blog'],
  ['blog-post', '/blog/hello-world'],
  ['docs', '/docs/getting-started'],
  ['changelog', '/changelog'],
  ['not-found', '/nope'],
  // The component gallery renders every token and component in every variant
  // on one URL, so this single baseline covers the whole design system.
  // Dev-only route (DEV_AUTH_BYPASS), which config refuses in production.
  ['gallery', '/dev/gallery'],
] as const;

const authedPages: Array<[string, string, 'pro' | 'admin']> = [
  ['dashboard', '/app', 'pro'],
  ['projects', '/app/projects', 'pro'],
  ['billing', '/app/settings/billing', 'pro'],
  ['notifications-prefs', '/app/settings/notifications', 'pro'],
  ['admin', '/admin', 'admin'],
  ['admin-announcements', '/admin/announcements', 'admin'],
  ['admin-audit', '/admin/audit', 'admin'],
  // The content editor (/admin/content/{id}) is deliberately absent: its
  // live-preview pane and revision timestamps are not stable pixels.
  ['admin-content', '/admin/content', 'admin'],
  // /admin/jobs is deliberately absent: the queue mutates between scrapes
  // (workers claim/complete rows), so its pixels are not a stable baseline.
  // a11y still covers the page.
  ['admin-schedules', '/admin/schedules', 'admin'],
  ['forbidden', '/admin', 'pro'],
];

for (const theme of ['light', 'dark'] as const) {
  test.describe(`visual (${theme})`, () => {
    for (const [name, path] of publicPages) {
      test(name, async ({ page }) => {
        if (theme === 'dark') {
          await page.addInitScript(() => localStorage.setItem('theme', 'dark'));
          await page.emulateMedia({ colorScheme: 'dark' });
        }
        await page.goto(path);
        await expect(page.locator('body')).toBeVisible();
        await expect(page).toHaveScreenshot(`${name}-${theme}.png`, {
          ...shot,
          // Every other baseline is the fold, which is what a visitor judges.
          // The gallery is a reference sheet, not a page: its whole value is
          // that nothing in the component layer escapes the frame.
          fullPage: name === 'gallery',
          mask: maskSelectors.map((s) => page.locator(s)),
        });
      });
    }

    for (const [name, path, user] of authedPages) {
      test(name, async ({ browser }) => {
        const context = await loginAs(browser, user);
        if (theme === 'dark') {
          await context.addInitScript(() => localStorage.setItem('theme', 'dark'));
        }
        const page = await context.newPage();
        if (theme === 'dark') {
          await page.emulateMedia({ colorScheme: 'dark' });
        }
        await page.goto(path);
        await expect(page.locator('main')).toBeVisible();
        await expect(page).toHaveScreenshot(`${name}-${theme}.png`, {
          ...shot,
          mask: maskSelectors.map((s) => page.locator(s)),
        });
        await context.close();
      });
    }
  });
}
