import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Split out of the projects spec: the activity page is its own module. The
// describe name is kept so the suite enumerates the same test title.
test.describe('projects', () => {
  test('activity page paginates', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/activity');

    await expect(page.getByText('Page 1 of')).toBeVisible();
    await page.getByRole('link', { name: 'Next →' }).click();
    await expect(page.getByText('Page 2 of')).toBeVisible();
    await expect(page).toHaveURL(/page=2/);
    await context.close();
  });
});
