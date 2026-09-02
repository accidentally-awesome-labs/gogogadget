import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// The enclosing describe name is kept from the original account spec so the
// suite still enumerates the same test titles after the split.
test.describe('account', () => {
  test('notification preferences persist across reload', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app/settings/notifications');

    const welcome = page.getByTestId('pref-welcome').locator('input');
    await expect(welcome).toBeChecked();
    await welcome.uncheck();
    await page.getByTestId('notification-prefs-form').getByRole('button').click();

    await expect(page.getByTestId('toast').first()).toBeVisible();
    await page.reload();
    await expect(page.getByTestId('pref-welcome').locator('input')).not.toBeChecked();
    await expect(page.getByTestId('pref-payment_failed').locator('input')).toBeChecked();

    // Restore default-on so re-runs start from the seeded state.
    await page.getByTestId('pref-welcome').locator('input').check();
    await page.getByTestId('notification-prefs-form').getByRole('button').click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });
});

test('digest cadence persists across reloads', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/settings/notifications');

  const select = page.getByTestId('digest-frequency');
  await expect(select).toBeVisible();
  await select.selectOption('daily');
  await page.getByTestId('notification-prefs-form').getByRole('button', { name: /save/i }).click();
  await expect(page.getByTestId('toast').first()).toBeVisible();

  await page.reload();
  await expect(page.getByTestId('digest-frequency')).toHaveValue('daily');

  // Opting out is the whole reason the control exists — prove it round-trips.
  await page.getByTestId('digest-frequency').selectOption('off');
  await page.getByTestId('notification-prefs-form').getByRole('button', { name: /save/i }).click();
  await expect(page.getByTestId('toast').first()).toBeVisible();
  await page.reload();
  await expect(page.getByTestId('digest-frequency')).toHaveValue('off');

  await context.close();
});
