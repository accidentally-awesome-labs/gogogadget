import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Billing flows: free org hits limits and the not-configured fragment; pro org
// sees its plan.
test.describe('billing', () => {
  test('free org: 4th project shows the upgrade CTA fragment', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();

    // org_free has 3 seeded projects (the free limit).
    await page.goto('/app/projects/new');
    await page.getByLabel('Name').fill('One too many');
    await page.getByRole('button', { name: 'Create project' }).click();

    await expect(page.getByTestId('plan-limit')).toBeVisible();
    await expect(page.getByTestId('plan-limit')).toContainText('Upgrade');
    await context.close();
  });

  test('free org: billing page shows usage and upgrade buttons', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app/settings/billing');

    await expect(page.getByTestId('usage-meter')).toContainText('3 / 3');
    await expect(page.getByRole('button', { name: 'Upgrade to Pro' })).toBeVisible();
    // No subscription → no manage button.
    await expect(page.getByTestId('manage-subscription')).toHaveCount(0);
    await context.close();
  });

  test('free org: checkout POST renders not-configured (keys empty in e2e)', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app/settings/billing');

    await page.getByRole('button', { name: 'Upgrade to Pro' }).click();
    await expect(page.getByText('Billing not configured')).toBeVisible();
    await expect(page.getByRole('link', { name: 'Setup guide →' })).toBeVisible();
    await context.close();
  });

  test('pro org: plan badge and manage button', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/settings/billing');

    await expect(page.getByTestId('plan-badge')).toContainText('active');
    await expect(page.getByText('Pro plan')).toBeVisible();
    await expect(page.getByTestId('manage-subscription')).toBeEnabled();
    // Unlimited plan → no usage meter.
    await expect(page.getByTestId('usage-meter')).toHaveCount(0);
    await context.close();
  });

  test('pro org: unlimited projects allowed past the free limit', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects/new');
    await page.getByLabel('Name').fill('Pro project');
    await page.getByRole('button', { name: 'Create project' }).click();
    await expect(page).toHaveURL(/\/app\/projects$/);
    await expect(page.getByTestId('project-row').filter({ hasText: 'Pro project' })).toBeVisible();
    await context.close();
  });
});
