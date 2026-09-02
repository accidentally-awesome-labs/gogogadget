import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Split out of the original admin spec: each admin module now owns the spec
// that drives its own surface. The describe name is kept so the suite
// enumerates the same test titles.
test.describe('admin', () => {
  test('audit viewer renders platform rows and filters', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/audit');

    await expect(page.getByTestId('audit-table')).toBeVisible();
    await expect(page.locator('[id^="audit-row-"]').first()).toBeVisible();

    await page.getByLabel('Search audit log').fill('project.created');
    await expect(page.locator('[id^="audit-row-"]').first()).toContainText('project.created');
    await context.close();
  });
});
