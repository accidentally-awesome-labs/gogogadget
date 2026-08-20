import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Notifications: badge count, page, read-all updates the badge (asserted via
// the badge endpoint — SSE flakiness is out of scope here).
test('badge, page, read-all flow', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();

  await page.goto('/app');
  const bell = page.getByTestId('notifications-bell');
  await expect(bell).toBeVisible();

  // Badge is a self-loading fragment (load trigger) — it appears even
  // without SSE.
  const badge = page.getByTestId('notifications-badge');
  await expect(badge.locator('span')).toBeVisible({ timeout: 10000 });

  await bell.click();
  await expect(page).toHaveURL('/app/notifications');
  await expect(page.getByTestId('notification-row').first()).toBeVisible();

  const before = await page.request.get('/app/notifications/badge');
  expect(before.ok()).toBeTruthy();
  expect(await before.text()).toContain('rounded-full');

  await page.getByTestId('notifications-read-all').click();
  // The click resolves on click, not on the htmx swap — poll the badge
  // until read-all lands (a single immediate read races the POST under
  // parallel-suite load and flakes).
  await expect
    .poll(async () => {
      const after = await page.request.get('/app/notifications/badge');
      expect(after.ok()).toBeTruthy();
      return (await after.text()).includes('rounded-full');
    })
    .toBe(false);

  await context.close();
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
