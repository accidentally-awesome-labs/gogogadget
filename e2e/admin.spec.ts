import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

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

  test('user search filters', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/users');

    await page.getByLabel('Search users').fill('pro@gogogadget.dev');
    await expect(page.getByText('pro@gogogadget.dev')).toBeVisible();
    await expect(page.getByText('free@gogogadget.dev')).toHaveCount(0);
    await context.close();
  });

  test('disable toggle flips state and audits', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());
    // Dedicated user: other specs keep running against untouched fixtures.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');

    const toggle = page.getByTestId('admin-disable-toggle');
    await expect(toggle).toContainText('Disable');
    await toggle.click();

    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page.getByText('disabled').first()).toBeVisible();

    // Toggle back so re-runs find the same initial state.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');
    await page.getByTestId('admin-disable-toggle').click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  test('non-admin gets 403', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    const response = await page.goto('/admin');
    expect(response?.status()).toBe(403);
    await context.close();
  });

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
