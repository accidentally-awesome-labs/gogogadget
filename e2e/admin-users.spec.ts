import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Split out of the original admin spec: each admin module now owns the spec
// that drives its own surface. The describe name is kept so the suite
// enumerates the same test titles.
test.describe('admin', () => {
  test('user search filters', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/admin/users');

    await page.getByLabel('Search users').fill('pro@gogogadget.dev');
    // Exact, and scoped to the cell. Row controls now name the user they act
    // on — the actions cell is labelled "Actions for pro@…" and its Disable
    // trigger "Disable pro@…" — so both a bare getByText and a substring cell
    // match resolve to several nodes. Naming the identity cell exactly is also
    // the stronger claim: the user is IN the table, not merely mentioned.
    await expect(page.getByRole('cell', { name: 'pro@gogogadget.dev', exact: true })).toBeVisible();
    await expect(page.getByRole('cell', { name: 'free@gogogadget.dev', exact: true })).toHaveCount(0);
    await context.close();
  });
});
