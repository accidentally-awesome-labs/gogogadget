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
  test('flags: create, override per org, delete', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    const key = 'flag-e2e-flow';

    // Create (retry-safe: delete leftovers from a failed prior run first).
    await page.goto('/admin/flags');
    const leftover = page.locator(`[data-testid="flag-delete-${key}"]`);
    if (await leftover.count()) {
      await leftover.click();
      await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
      await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(0);
    }

    await page.getByTestId('flag-create-form').locator('#key').fill(key);
    await page.getByTestId('flag-create-form').locator('#description').fill('e2e flow flag');
    await page.getByTestId('flag-create-form').getByRole('button', { name: 'Create flag' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();

    // Detail: set an ON override for Free Org (global is off).
    await page.goto(`/admin/flags/${key}`);
    await page.locator('#org').selectOption({ label: 'Free Org' });
    await page.locator('#state').selectOption('on');
    await page.getByTestId('flag-override-form').getByRole('button', { name: 'Set override' }).click();
    await expect(page.getByTestId('flag-override-org_free')).toBeVisible();
    await expect(page.getByTestId('flag-override-org_free')).toContainText('on');

    // Delete the flag — overrides cascade.
    await page.goto('/admin/flags');
    await page.getByTestId(`flag-delete-${key}`).click();
    // The flag survives an open confirmation: the request rides on Confirm.
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(1);
    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(0);
    await context.close();
  });

  // One cancel case for the whole admin slice. Every destructive admin control
  // is the same ui.ConfirmAction, so covering the decline path once per slice
  // proves the mechanism; repeating it per row would only re-test the
  // component. A confirmation nobody can decline is theatre.
  test('cancelling a destructive confirmation leaves the row alone', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    const key = 'flag-e2e-cancel';

    await page.goto('/admin/flags');
    await page.getByTestId('flag-create-form').locator('#key').fill(key);
    await page.getByTestId('flag-create-form').locator('#description').fill('e2e cancel flag');
    await page.getByTestId('flag-create-form').getByRole('button', { name: 'Create flag' }).click();
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(1);

    // The ConfirmAction root wraps the trigger AND the dialog, so getByRole
    // matches the dialog's cancel and confirm buttons too once the dialog is
    // rendered. The trigger has its own hook.
    const trigger = page.getByTestId(`flag-delete-${key}`).locator('[data-ui-confirm-trigger]');
    await trigger.click();
    await expect(openDialog(page)).toBeVisible();
    await openDialog(page).getByRole('button', { name: 'Cancel' }).click();

    // Dismissed, no request issued, flag still there.
    await expect(openDialog(page)).toHaveCount(0);
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(1);
    // Focus returns to the control that opened the dialog, or a keyboard user
    // is dropped at the top of the document with no way back to the row.
    await expect(trigger).toBeFocused();

    // Reload proves the cancel was not merely a client-side no-op that the
    // server had already acted on.
    await page.goto('/admin/flags');
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(1);

    // Clean up through the confirm path.
    await page.getByTestId(`flag-delete-${key}`).click();
    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(0);
    await context.close();
  });
});

// Split out of the original admin spec's support-staff case: the flags admin is
// its own module, so the read-only assertions on its surface live with it.
test('support staff read the flags admin without the controls', async ({ browser }) => {
  const context = await loginAs(browser, 'support');
  const page = await context.newPage();

  // …the data is there…
  await page.goto('/admin/flags');
  await expect(page.getByTestId('flags-table')).toBeVisible();

  // …but nothing that mutates is offered.
  await expect(page.getByTestId('flag-create-form')).toHaveCount(0);

  await context.close();
});
