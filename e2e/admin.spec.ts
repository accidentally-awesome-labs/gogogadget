import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

test.describe('admin', () => {
  test('admin home renders stat cards', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin');

    await expect(page.getByTestId('stat-card')).toHaveCount(4);
    await expect(page.getByText('Total users')).toBeVisible();
    await expect(page.getByText('MRR')).toBeVisible();
    await expect(page.getByText('Signups, last 30 days')).toBeVisible();
    await context.close();
  });

  test('user search filters', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/users');

    await page.getByLabel('Search users').fill('pro@gogogadget.dev');
    await expect(page.getByText('pro@gogogadget.dev')).toBeVisible();
    await expect(page.getByText('free@gogogadget.dev')).toHaveCount(0);
    await context.close();
  });

  test('disable toggle flips state and audits', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());
    // Dedicated user: other specs keep running against untouched fixtures.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');

    const toggle = page.getByTestId('admin-disable-toggle');
    await expect(toggle).toContainText('Disable');
    await toggle.click();

    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page.getByText('disabled').first()).toBeVisible();

    // Toggle back so re-runs find the same initial state.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');
    await page.getByTestId('admin-disable-toggle').click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  test('non-admin gets 403', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    const response = await page.goto('/admin');
    expect(response?.status()).toBe(403);
    await context.close();
  });

  test('orgs list shows member counts and plan badges', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/orgs');

    await expect(page.getByText('Free Org')).toBeVisible();
    await expect(page.getByText('Pro Org')).toBeVisible();
    await expect(page.getByTestId('plan-badge').filter({ hasText: 'pro' })).toBeVisible();
    await context.close();
  });

  test('audit viewer renders platform rows and filters', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/audit');

    await expect(page.getByTestId('audit-table')).toBeVisible();
    await expect(page.locator('[id^="audit-row-"]').first()).toBeVisible();

    await page.getByLabel('Search audit log').fill('project.created');
    await expect(page.locator('[id^="audit-row-"]').first()).toContainText('project.created');
    await context.close();
  });

  test('jobs viewer shows kind and status', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/jobs');

    // The queue is live state (other specs enqueue export jobs) — the table
    // itself with its header row is the stable contract.
    await expect(page.getByTestId('jobs-table').or(page.getByText('No jobs match.'))).toBeVisible();
    await context.close();
  });

  test('announcements: create, activate, banner, dismiss, deactivate', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());

    // Create (inactive).
    await page.goto('/admin/announcements');
    await page.getByTestId('announcement-create-form').locator('#announcement-message').fill('E2E maintenance window');
    await page.getByTestId('announcement-create-form').getByRole('button', { name: 'Create' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    // Retry-safe: a previous failed run may have left rows behind — always
    // operate on the newest and clean every match at the end.
    const row = page.locator('[id^="announcement-row-"]').filter({ hasText: 'E2E maintenance window' }).last();
    await expect(row).toBeVisible();

    // Activate → banner on /app.
    await row.getByRole('button', { name: 'Activate' }).click();
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
      await expect(page.locator(`[id="${rowId}"]`)).toHaveCount(0);
    }
    await expect(stale()).toHaveCount(0);
    await context.close();
  });

  test('flags: create, override per org, delete', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());
    const key = 'flag-e2e-flow';

    // Create (retry-safe: delete leftovers from a failed prior run first).
    await page.goto('/admin/flags');
    const leftover = page.locator(`[data-testid="flag-delete-${key}"]`);
    if (await leftover.count()) await leftover.click();

    await page.getByTestId('flag-create-form').locator('#flag-key').fill(key);
    await page.getByTestId('flag-create-form').locator('#flag-description').fill('e2e flow flag');
    await page.getByTestId('flag-create-form').getByRole('button', { name: 'Create flag' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();

    // Detail: set an ON override for Free Org (global is off).
    await page.goto(`/admin/flags/${key}`);
    await page.locator('#override-org').selectOption({ label: 'Free Org' });
    await page.locator('#override-state').selectOption('on');
    await page.getByTestId('flag-override-form').getByRole('button', { name: 'Set override' }).click();
    await expect(page.getByTestId('flag-override-org_free')).toBeVisible();
    await expect(page.getByTestId('flag-override-org_free')).toContainText('on');

    // Delete the flag — overrides cascade.
    await page.goto('/admin/flags');
    await page.getByTestId(`flag-delete-${key}`).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page.getByTestId(`flag-toggle-${key}`)).toHaveCount(0);
    await context.close();
  });

  test('schedules: create, run now, toggle, delete', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());

    await page.goto('/admin/schedules');
    await expect(page.getByTestId('schedules-table')).toBeVisible();

    // Create a system-wide hourly schedule.
    await page.getByTestId('schedule-create-form').locator('#schedule-name').fill('E2E flush');
    await page.getByTestId('schedule-create-form').locator('#schedule-kind').selectOption('usage.flush');
    await page.getByTestId('schedule-create-form').locator('#schedule-every').fill('3600');
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
      await expect(page.getByTestId('toast').first()).toBeVisible();
      await expect(page.locator(`[data-testid="${rowId}"]`)).toHaveCount(0);
    }
    await page.goto('/admin/schedules');
    await expect(stale()).toHaveCount(0);
    await context.close();
  });
});
