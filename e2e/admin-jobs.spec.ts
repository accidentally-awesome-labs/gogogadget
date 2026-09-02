import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Split out of the original admin spec: each admin module now owns the spec
// that drives its own surface. The describe name is kept so the suite
// enumerates the same test titles.
test.describe('admin', () => {
  test('jobs viewer shows kind and status', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/jobs');

    // The DataTable surface is always rendered now — it keeps the toolbar and
    // pager in place when a filter matches nothing — so `.or(empty text)` used
    // to resolve to both nodes and trip strict mode whenever the queue drained.
    // Assert the surface, then assert it settled into exactly one of its two
    // legitimate states. The queue itself is live: other specs enqueue export
    // jobs, so a row count is not a contract.
    await expect(page.getByTestId('jobs-table')).toBeVisible();
    await expect(
      page.locator('[id^="job-row-"]').first().or(page.getByText('No jobs match.')),
    ).toBeVisible();
    await context.close();
  });
});
