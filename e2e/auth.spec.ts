import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Auth flows against the DEV_AUTH_BYPASS synthetic tokens — every guard and
// middleware still executes.
test.describe('auth', () => {
  test('anonymous /app redirects to /login', async ({ page }) => {
    // Assert the guard's 303 directly (the browser would keep following the
    // dev-login chain in bypass mode).
    const response = await page.request.get('/app', { maxRedirects: 0 });
    expect(response.status()).toBe(303);
    expect(response.headers()['location']).toBe('/login');
  });

  test('free user lands on the dashboard with their org', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app');
    await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
    await expect(page.getByTestId('stat-card').first()).toBeVisible();
    await context.close();
  });

  test('user with zero orgs is sent to create one', async ({ browser }) => {
    const context = await loginAs(browser, 'noorg');
    const page = await context.newPage();
    // The 303 targets the portal's create-organization (an invited teammate
    // must never be told to found a competing org). The host is fake in e2e,
    // so assert on the attempted navigation, not a successful load.
    const request = page.waitForRequest((req) =>
      req.url().includes('accounts.example.test/create-organization'),
    );
    await page.goto('/app').catch(() => {});
    await request;
    await context.close();
  });

  test('member with no active org sees SelectOrg', async ({ browser }) => {
    const context = await loginAs(browser, 'noactive');
    const page = await context.newPage();
    await page.goto('/app');
    await expect(page.getByRole('heading', { name: 'Choose an organization' })).toBeVisible();
    await expect(page.getByTestId('select-org-button').first()).toBeVisible();
    await expect(page.getByText('Free Org')).toBeVisible();
    await context.close();
  });

  test('disabled user gets the 403 disabled page', async ({ browser }) => {
    const context = await loginAs(browser, 'disabled');
    const page = await context.newPage();
    const response = await page.goto('/app');
    expect(response?.status()).toBe(403);
    await expect(page.getByRole('heading', { name: 'Account disabled' })).toBeVisible();
    await context.close();
  });
});
