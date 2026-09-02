import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Billing flows on the billing settings page: usage and upgrade CTAs, the
// local checkout confirmation step, and the pro org's plan. The plan-limit
// cases live in project-plan-limits.spec.ts with the projects workflow.
test.describe('billing', () => {
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

  // development/test select the billing-local adapter, so checkout is real: it
  // hands the org a local confirmation step instead of a provider redirect.
  // Confirming would flip org_free to pro and break the sibling free-limit
  // tests, so this asserts the screen and stops there.
  test('free org: checkout POST lands on the local confirmation step', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app/settings/billing');

    await page.getByRole('button', { name: 'Upgrade to Pro' }).click();
    await expect(page).toHaveURL(/\/app\/billing\/confirm\?.*product=/);
    await expect(page).toHaveURL(/customer=org_free/);
    await expect(page.getByRole('heading', { name: 'Confirm billing' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Confirm' })).toBeVisible();
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
});
