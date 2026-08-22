import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { loginAs } from './helpers';

// axe over the visual-spec pages, both themes; serious/critical fails the run.
const publicPages = ['/', '/pricing', '/blog', '/blog/hello-world', '/docs/getting-started', '/changelog', '/nope'];

for (const theme of ['light', 'dark'] as const) {
  test.describe(`a11y (${theme})`, () => {
    for (const path of publicPages) {
      test(`public ${path}`, async ({ page }) => {
        if (theme === 'dark') {
          await page.addInitScript(() => localStorage.setItem('theme', 'dark'));
        }
        await page.goto(path);
        const results = await new AxeBuilder({ page }).analyze();
        const violations = results.violations.filter(
          (v) => v.impact === 'serious' || v.impact === 'critical',
        );
        expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
      });
    }

    const authedPages: Array<[string, 'pro' | 'admin']> = [
      ['/app', 'pro'],
      ['/app/projects', 'pro'],
      ['/app/settings/billing', 'pro'],
      ['/app/settings/notifications', 'pro'],
      ['/admin', 'admin'],
      ['/admin/announcements', 'admin'],
      ['/admin/audit', 'admin'],
      ['/admin/content', 'admin'],
      ['/admin/jobs', 'admin'],
      ['/admin/media', 'admin'],
      ['/admin/schedules', 'admin'],
    ];
    for (const [path, user] of authedPages) {
      test(`authed ${path}`, async ({ browser }) => {
        const context = await loginAs(browser, user);
        if (theme === 'dark') {
          await context.addInitScript(() => localStorage.setItem('theme', 'dark'));
        }
        const page = await context.newPage();
        await page.goto(path);
        const results = await new AxeBuilder({ page }).analyze();
        const violations = results.violations.filter(
          (v) => v.impact === 'serious' || v.impact === 'critical',
        );
        expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
        await context.close();
      });
    }
  });
}
