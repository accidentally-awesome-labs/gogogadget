import { test, expect, type Page } from '@playwright/test';
import { loginAs } from './helpers';

// HTMX CRUD against the pro org (5 seeded projects, unlimited plan).
test.describe('projects', () => {
  // Unique names per run: a retried test must not collide with its own
  // previous attempt's rows.
  const uniq = (base: string) => `${base} ${Math.random().toString(36).slice(2, 8)}`;

  test('create soft-navigates: shell and mounted widgets survive', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    const name = uniq('Foxtrot');
    await page.goto('/app/projects/new');
    await expect(page).toHaveTitle(/^New project/);

    // Mark the live document and plant a node directly on <body> — where
    // clerk-js renders its dropdown portals. A full page load wipes both; the
    // HX-Location navigation the server sends must not. The marker is a dataset
    // entry on <html> so it needs no window typing, and <html> is outside every
    // swap target just like the shell is.
    await page.evaluate(() => {
      document.documentElement.dataset.e2eAlive = 'yes';
      const sentinel = document.createElement('div');
      sentinel.id = 'body-portal-sentinel';
      document.body.appendChild(sentinel);
    });

    await page.getByLabel('Name').fill(name);
    await page.getByRole('button', { name: 'Create project' }).click();

    await expect(page).toHaveURL(/\/app\/projects$/);
    await expect(page.getByTestId('project-row').filter({ hasText: name }).first()).toBeVisible();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    // The response was a full page selected down to #content, so the document
    // title tracks the destination…
    await expect(page).toHaveTitle(/^Projects/);
    // …and the whole page arrived: heading and search box, not just the table.
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await expect(page.getByLabel('Search projects')).toBeVisible();
    // …while nothing outside #content was touched.
    await expect.poll(() =>
      page.evaluate(() => ({
        alive: document.documentElement.dataset.e2eAlive === 'yes',
        sentinel: !!document.getElementById('body-portal-sentinel'),
        contents: document.querySelectorAll('#content').length,
        nested: /<html|<body/i.test(document.getElementById('content')?.innerHTML ?? ''),
      })),
    ).toEqual({ alive: true, sentinel: true, contents: 1, nested: false });
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

  test('search morphs the table, keeping surviving row nodes', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects');

    // Hold a handle to the row that survives the filter. innerMorph patches the
    // table in place and matches rows by id, so this exact node must be reused
    // rather than re-created — that is what keeps focus and selection intact
    // while typing. A replaced node would be detached from the document, so
    // isConnected is the identity proof.
    const survivor = await page
      .getByTestId('project-row')
      .filter({ hasText: 'Alpha' })
      .first()
      .elementHandle();
    expect(survivor).not.toBeNull();

    await page.getByLabel('Search projects').fill('Alpha');
    await expect(page.getByTestId('project-row')).toHaveCount(1);
    await expect(page.getByTestId('project-row').first()).toContainText('Alpha');
    await expect.poll(() => survivor?.evaluate((row) => row.isConnected)).toBe(true);
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

  // The confirmation is an in-page AlertDialog, not window.confirm: there is
  // deliberately no page.on('dialog') handler here, so a regression that
  // resurrects the native prompt hangs this test instead of passing quietly.
  // Scoped to [open] because one dialog exists per row.
  const openDialog = (page: Page) => page.locator('dialog[data-ui="alert-dialog"][open]');

  test('delete with confirm removes the row', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects');

    const row = page.getByTestId('project-row').filter({ hasText: 'Delta' });
    await row.getByRole('button', { name: 'Delete' }).click();

    // The whole point of a confirmation is that the destructive thing has NOT
    // happened yet. Assert that before confirming, or a broken ConfirmAction
    // that deletes on the first click passes this test.
    await expect(openDialog(page)).toBeVisible();
    await expect(row).toHaveCount(1);

    await openDialog(page).getByRole('button', { name: 'Delete permanently' }).click();

    await expect(row).toHaveCount(0);
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  // A confirmation nobody can decline is theatre. Bravo is touched by no other
  // case, so this one is safe to run in parallel with the rest of the file.
  test('cancelling the delete dialog keeps the row and returns focus', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/projects');

    const row = page.getByTestId('project-row').filter({ hasText: 'Bravo' });
    const trigger = row.getByRole('button', { name: 'Delete' });
    await trigger.click();
    await expect(openDialog(page)).toBeVisible();

    await openDialog(page).getByRole('button', { name: 'Cancel' }).click();

    await expect(openDialog(page)).toHaveCount(0);
    await expect(row).toHaveCount(1);
    // Focus returns to the control that opened it — a keyboard user who
    // declines must not be stranded at the top of the document.
    await expect(trigger).toBeFocused();
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
