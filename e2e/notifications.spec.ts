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
  const after = await page.request.get('/app/notifications/badge');
  expect(after.ok()).toBeTruthy();
  expect(await after.text()).not.toContain('rounded-full');

  await context.close();
});
