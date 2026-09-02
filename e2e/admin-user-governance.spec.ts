import { test, expect, type Page } from '@playwright/test';
import { loginAs } from './helpers';

// Destructive admin actions are gated by ui.ConfirmAction, which opens a real
// in-page <dialog> instead of window.confirm. These specs therefore never
// register a page.on('dialog') handler: if one were left in place, a
// regression that resurrected the native confirm would pass silently.
//
// The locator is scoped to [open] deliberately. A table renders one dialog per
// destructive row, so [data-ui="alert-dialog"] alone matches every row and
// trips strict mode; [open] is both unique and the state being asserted.
const openDialog = (page: Page) => page.locator('dialog[data-ui="alert-dialog"][open]');

// Split out of the original admin spec: each admin module now owns the spec
// that drives its own surface. The describe name is kept so the suite
// enumerates the same test titles.
test.describe('admin', () => {
  test('disable toggle flips state and audits', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    // Dedicated user: other specs keep running against untouched fixtures.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');

    const toggle = page.getByTestId('admin-disable-toggle');
    await expect(toggle).toContainText('Disable');
    await toggle.click();

    // The trigger must only OPEN the confirmation. Asserting the account is
    // still active here is the whole point: a ConfirmAction that fired the
    // request on the first click would satisfy every assertion below it, so
    // without this the confirmation would be a rubber stamp.
    await expect(openDialog(page)).toBeVisible();
    await expect(page.getByTestId('toast')).toHaveCount(0);
    await expect(page.getByText('active').first()).toBeVisible();

    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page.getByText('disabled').first()).toBeVisible();

    // Toggle back so re-runs find the same initial state.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');
    await page.getByTestId('admin-disable-toggle').click();
    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });
});

// The flags-surface half of this case moved to admin-flags.spec.ts with the
// module that owns it; every assertion is unchanged.
test('support staff read the admin area without the controls', async ({ browser }) => {
  const context = await loginAs(browser, 'support');
  const page = await context.newPage();

  // The dashboards are readable…
  for (const path of ['/admin', '/admin/users', '/admin/flags', '/admin/audit']) {
    await page.goto(path);
    await expect(page.getByRole('heading').first()).toBeVisible();
  }

  // …but nothing that mutates is offered.
  await page.goto('/admin/users');
  await expect(page.getByTestId('admin-impersonate')).toHaveCount(0);
  await expect(page.getByTestId('admin-disable-toggle')).toHaveCount(0);
  await expect(page.getByTestId('admin-readonly').first()).toBeVisible();

  // And the boundary is enforced server-side, not just hidden in the UI.
  const resp = await page.request.post('/admin/users/user_pro/disable');
  expect(resp.status()).toBe(403);

  await context.close();
});
