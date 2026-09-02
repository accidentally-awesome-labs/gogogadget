import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Split out of the original admin spec: each admin module now owns the spec
// that drives its own surface. The describe name is kept so the suite
// enumerates the same test titles.
test.describe('admin', () => {
  test('admin home renders stat cards', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin');

    await expect(page.getByTestId('stat-card')).toHaveCount(4);
    await expect(page.getByText('Total users')).toBeVisible();
    await expect(page.getByText('MRR')).toBeVisible();
    await expect(page.getByText('Signups, last 30 days')).toBeVisible();
    await context.close();
  });

  test('non-admin gets 403', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    const response = await page.goto('/admin');
    expect(response?.status()).toBe(403);
    await context.close();
  });
});
