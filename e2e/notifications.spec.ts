import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Notifications: badge count, page, read-all, and the live stream.
test('badge, page, read-all flow', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();

  await page.goto('/app');
  const bell = page.getByTestId('notifications-bell');
  await expect(bell).toBeVisible();

  // Badge is a self-loading fragment (load trigger).
  const badge = page.getByTestId('notifications-badge');
  await expect(badge.locator('span')).toBeVisible({ timeout: 10000 });

  await bell.click();
  await expect(page).toHaveURL('/app/notifications');
  await expect(page.getByTestId('notification-row').first()).toBeVisible();

  // Ask for the fragment the way htmx does, so this asserts on the bubble
  // itself rather than on a full page that happens to contain the class.
  const asFragment = { headers: { 'HX-Request': 'true', 'HX-Request-Type': 'partial' } };
  const before = await page.request.get('/app/notifications/badge', asFragment);
  expect(before.ok()).toBeTruthy();
  expect(await before.text()).toContain('count-badge');

  await page.getByTestId('notifications-read-all').click();
  // The click resolves on click, not on the htmx swap — poll the badge
  // until read-all lands (a single immediate read races the POST under
  // parallel-suite load and flakes).
  await expect
    .poll(async () => {
      const after = await page.request.get('/app/notifications/badge', asFragment);
      expect(after.ok()).toBeTruthy();
      return (await after.text()).includes('count-badge');
    })
    .toBe(false);

  await context.close();
});
// The live stream is the feature the docs promise, and it was silently dead
// for as long as the htmx 2 SSE extension was vendored against htmx 4: the
// script threw on load, the EventSource was never opened, and the badge only
// ever updated on navigation.
//
// This asserts the thing that broke — a real connection to the stream, and a
// page that loads without throwing. It deliberately does NOT wait for a count
// to change: producing a notification means waiting on the shared job worker,
// which under a parallel suite is a timing race rather than a signal. The
// count-changing behaviour is verified against a live server during
// development (badge 4 → 5 in ~3s with no navigation).
test('the notification stream actually connects', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();

  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  const streamRequests: string[] = [];
  page.on('request', (r) => {
    if (r.url().includes('/app/notifications/stream')) streamRequests.push(r.resourceType());
  });

  await page.goto('/app');
  // Wait for the shell, not the badge: whether the badge renders a count
  // depends on unread state another test in this file mutates.
  await expect(page.getByTestId('notifications-bell')).toBeVisible();

  // The carrier element lives in the shell and app.js opens the connection.
  await expect
    .poll(() => streamRequests.length, { timeout: 10000 })
    .toBeGreaterThan(0);
  expect(streamRequests[0]).toBe('eventsource');

  // A broken SSE client throws on load — that is exactly how this shipped.
  expect(errors).toEqual([]);

  // The connection survives boosted navigation, because the shell is never
  // swapped: no second connection is opened.
  await page.getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page).toHaveURL(/\/app\/projects$/);
  expect(streamRequests, 'one connection, reused across navigation').toHaveLength(1);

  await context.close();
});
