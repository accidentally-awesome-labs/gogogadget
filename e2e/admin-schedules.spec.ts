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
  test('schedules: create, run now, toggle, delete', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();

    await page.goto('/admin/schedules');
    await expect(page.getByTestId('schedules-table')).toBeVisible();

    // Create a system-wide hourly schedule.
    await page.getByTestId('schedule-create-form').locator('#name').fill('E2E flush');
    await page.getByTestId('schedule-create-form').locator('#kind').selectOption('usage.flush');
    await page.getByTestId('schedule-create-form').locator('#every_seconds').fill('3600');
    await page.getByTestId('schedule-create-form').getByRole('button', { name: 'Create schedule' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();

    const row = page.locator('[data-testid^="schedule-row-"]').filter({ hasText: 'E2E flush' }).last();
    await expect(row).toBeVisible();
    await expect(row).toContainText('usage.flush');
    await expect(row).toContainText('system');

    // Run now, then toggle off (the run button disappears when disabled).
    await row.getByRole('button', { name: 'Run now' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    const again = page.locator('[data-testid^="schedule-row-"]').filter({ hasText: 'E2E flush' }).last();
    await again.getByRole('button', { name: 'on' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();

    // Clean up every leftover row so re-runs start from the seeded state.
    // Delete-while-any (never a precomputed count): each Navigate re-renders
    // the table, and a prior failed run can leave extra rows behind.
    // Reload before each delete: clicking into a table that an in-flight
    // HX-Location swap is replacing lands on a detached row and silently
    // does nothing (observed flake).
    const stale = () => page.locator('[data-testid^="schedule-row-"]').filter({ hasText: 'E2E flush' });
    for (let guard = 0; guard < 10; guard++) {
      await page.goto('/admin/schedules');
      if ((await stale().count()) === 0) break;
      const rowId = await stale().first().getAttribute('data-testid');
      await stale().first().getByRole('button', { name: 'Delete' }).click();
      // Row intact while the confirmation is open.
      await expect(page.locator(`[data-testid="${rowId}"]`)).toHaveCount(1);
      await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
      await expect(page.getByTestId('toast').first()).toBeVisible();
      await expect(page.locator(`[data-testid="${rowId}"]`)).toHaveCount(0);
    }
    await page.goto('/admin/schedules');
    await expect(stale()).toHaveCount(0);
    await context.close();
  });
});
