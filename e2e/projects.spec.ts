import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// HTMX CRUD against the pro org (5 seeded projects, unlimited plan).
test.describe('projects', () => {
  // Unique names per run: a retried test must not collide with its own
  // previous attempt's rows.
  const uniq = (base: string) => `${base} ${Math.random().toString(36).slice(2, 8)}`;

  test('create appends without a full reload', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    const name = uniq('Foxtrot');
    await page.goto('/app/projects');

    await page.getByRole('link', { name: 'New project' }).click();
    await page.getByLabel('Name').fill(name);
    await page.getByRole('button', { name: 'Create project' }).click();

    await expect(page).toHaveURL(/\/app\/projects$/);
    await expect(page.getByTestId('project-row').filter({ hasText: name }).first()).toBeVisible();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  test('empty name renders the inline 422 error', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects/new');

    await page.getByLabel('Name').fill('');
    await page.getByRole('button', { name: 'Create project' }).click();

    await expect(page.getByTestId('form-error')).toBeVisible();
    await expect(page.getByTestId('form-error')).toContainText('Name is required');
    // URL unchanged: the form re-rendered as a fragment, not a navigation.
    await expect(page).toHaveURL(/\/app\/projects\/new$/);
    await context.close();
  });

  test('search filters rows', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects');

    await page.getByLabel('Search projects').fill('Alpha');
    await expect(page.getByTestId('project-row')).toHaveCount(1);
    await expect(page.getByTestId('project-row').first()).toContainText('Alpha');
    await context.close();
  });

  test('archive removes from the default list', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects');

    const row = page.getByTestId('project-row').filter({ hasText: 'Echo' });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'Archive' }).click();

    await expect(row).toHaveCount(0);
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  test('delete with confirm removes the row', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());
    await page.goto('/app/projects');

    const row = page.getByTestId('project-row').filter({ hasText: 'Delta' });
    await row.getByRole('button', { name: 'Delete' }).click();

    await expect(row).toHaveCount(0);
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

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
