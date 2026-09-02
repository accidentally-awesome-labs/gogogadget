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
  test('announcements: create, activate, banner, dismiss, deactivate', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();

    // Create (inactive).
    await page.goto('/admin/announcements');
    await page.getByTestId('announcement-create-form').locator('#message').fill('E2E maintenance window');
    await page.getByTestId('announcement-create-form').getByRole('button', { name: 'Create' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    // Retry-safe: a previous failed run may have left rows behind — always
    // operate on the newest and clean every match at the end.
    const row = page.locator('[id^="announcement-row-"]').filter({ hasText: 'E2E maintenance window' }).last();
    await expect(row).toBeVisible();

    // Activate → banner on /app. Wait for the row to flip before navigating:
    // going straight to /app races the in-flight POST, and the banner is only
    // there if the activation landed first.
    await row.getByRole('button', { name: 'Activate' }).click();
    await expect(row.getByRole('button', { name: 'Deactivate' })).toBeVisible();
    await page.goto('/app');
    await expect(page.getByTestId('announcement-banner')).toContainText('E2E maintenance window');

    // Dismiss hides it for this browser (per-announcement key).
    await page.getByTestId('announcement-banner').getByRole('button').click();
    await expect(page.getByTestId('announcement-banner')).toBeHidden();
    await page.goto('/app');
    await expect(page.getByTestId('announcement-banner')).toBeHidden();

    // Deactivate → banner gone; delete EVERY matching row so re-runs start
    // clean. Delete-while-any (never a precomputed count): each Navigate
    // re-renders the table, so a fixed loop bound races the re-render.
    await page.goto('/admin/announcements');
    const stale = () => page.locator('[id^="announcement-row-"]').filter({ hasText: 'E2E maintenance window' });
    for (let guard = 0; guard < 10 && (await stale().count()) > 0; guard++) {
      const current = stale().first();
      const rowId = await current.getAttribute('id');
      if (await current.getByRole('button', { name: 'Deactivate' }).count()) {
        await current.getByRole('button', { name: 'Deactivate' }).click();
        await expect(page.getByTestId('toast').first()).toBeVisible();
      }
      await current.getByRole('button', { name: 'Delete' }).click();
      // Still present while the confirmation is up — the delete has not run yet.
      await expect(page.locator(`[id="${rowId}"]`)).toHaveCount(1);
      await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
      await expect(page.locator(`[id="${rowId}"]`)).toHaveCount(0);
    }
    await expect(stale()).toHaveCount(0);
    await context.close();
  });
});
