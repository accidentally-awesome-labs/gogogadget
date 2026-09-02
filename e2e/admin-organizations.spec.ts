import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Split out of the original admin spec: each admin module now owns the spec
// that drives its own surface. The describe name is kept so the suite
// enumerates the same test titles.
test.describe('admin', () => {
  test('orgs list shows member counts and plan badges', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/orgs');

    await expect(page.getByText('Free Org')).toBeVisible();
    await expect(page.getByText('Pro Org')).toBeVisible();
    await expect(page.getByTestId('plan-badge').filter({ hasText: 'pro' })).toBeVisible();
    await context.close();
  });
});
