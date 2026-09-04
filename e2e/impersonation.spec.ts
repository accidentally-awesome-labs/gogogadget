import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Admin impersonation: admin views as the pro user → banner → exit → admin.
test('impersonate, banner, exit', async ({ browser }) => {
  const context = await loginAs(browser, 'admin');
  const page = await context.newPage();

  await page.goto('/admin/users');
  const row = page.locator('tr#user-user_pro');
  await row.getByTestId('admin-impersonate').click();

  // Interstitial: impersonation needs a stated reason (audited).
  await expect(page.getByTestId('impersonate-form')).toBeVisible();
  await page.getByTestId('impersonate-start').click();
  await expect(page.getByTestId('impersonate-error')).toBeVisible();
  await page.getByTestId('impersonate-reason').fill('Ticket #7 — reproducing the reported export failure');
  await page.getByTestId('impersonate-start').click();

  // Landed in the app as the target, banner visible, target org in shell
  // (the test environment selects the zero-account identity adapter, which
  // contributes no org-switcher widget, so the shell renders its own neutral
  // mount container and names the org it stands in for).
  await expect(page.getByTestId('impersonation-banner')).toBeVisible();
  await expect(page.locator('#org-switcher')).toHaveAttribute('data-shell-placeholder', 'Pro Org');

  // /admin is correctly forbidden mid-impersonation.
  const resp = await page.request.get('/admin');
  expect(resp.status()).toBe(403);

  // Exit → back on /admin as the admin, banner gone.
  await page.getByTestId('impersonation-exit').click();
  await page.waitForURL('/admin');
  await expect(page.getByText('Total users')).toBeVisible();
  await expect(page.getByTestId('impersonation-banner')).toBeHidden();

  await context.close();
});
