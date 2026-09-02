import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Plan limits on the projects surface: the free org is refused its 4th
// project and the pro org is not. Split out of the billing spec so the
// projects workflow owns the spec that drives its own pages; the describe
// name is kept so the suite enumerates the same test titles.
test.describe('billing', () => {
  test('free org: 4th project shows the upgrade CTA fragment', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();

    // org_free has 3 seeded projects (the free limit).
    await page.goto('/app/projects/new');
    await page.getByLabel('Name').fill('One too many');
    await page.getByRole('button', { name: 'Create project' }).click();

    await expect(page.getByTestId('plan-limit')).toBeVisible();
    await expect(page.getByTestId('plan-limit')).toContainText('Upgrade');
    await context.close();
  });

  test('pro org: unlimited projects allowed past the free limit', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects/new');
    await page.getByLabel('Name').fill('Pro project');
    await page.getByRole('button', { name: 'Create project' }).click();
    await expect(page).toHaveURL(/\/app\/projects$/);
    await expect(page.getByTestId('project-row').filter({ hasText: 'Pro project' })).toBeVisible();
    await context.close();
  });
});
