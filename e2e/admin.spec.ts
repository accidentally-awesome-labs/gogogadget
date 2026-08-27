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
    // Exact, and scoped to the cell. Row controls now name the user they act
    // on — the actions cell is labelled "Actions for pro@…" and its Disable
    // trigger "Disable pro@…" — so both a bare getByText and a substring cell
    // match resolve to several nodes. Naming the identity cell exactly is also
    // the stronger claim: the user is IN the table, not merely mentioned.
    await expect(page.getByRole('cell', { name: 'pro@gogogadget.dev', exact: true })).toBeVisible();
    await expect(page.getByRole('cell', { name: 'free@gogogadget.dev', exact: true })).toHaveCount(0);
    await context.close();
  });

  test('disable toggle flips state and audits', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    // Dedicated user: other specs keep running against untouched fixtures.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');

    const toggle = page.getByTestId('admin-disable-toggle');
    await expect(toggle).toContainText('Disable');
    await toggle.click();

    // The trigger must only OPEN the confirmation. Asserting the account is
    // still active here is the whole point: a ConfirmAction that fired the
    // request on the first click would satisfy every assertion below it, so
    // without this the confirmation would be a rubber stamp.
    await expect(openDialog(page)).toBeVisible();
    await expect(page.getByTestId('toast')).toHaveCount(0);
    await expect(page.getByText('active').first()).toBeVisible();

    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page.getByText('disabled').first()).toBeVisible();

    // Toggle back so re-runs find the same initial state.
    await page.goto('/admin/users?q=toggle@gogogadget.dev');
    await page.getByTestId('admin-disable-toggle').click();
    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
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

test.describe('content', () => {
  test('post: create, live preview, publish, restore a revision, delete', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();

    // Parallel workers share one e2e database, so the title (and the slug it
    // derives) must be unique per run: a fixed slug hits the
    // (kind, slug, locale) unique index and 422s instead of creating.
    const stamp = Date.now();
    const title = `CMS smoke ${stamp}`;
    const slug = `cms-smoke-${stamp}`;
    // The edited title deliberately shares no words with the original, so a
    // hasText filter for one never matches the other.
    const edited = `CMS rewrite ${stamp}`;

    await page.goto('/admin/content');
    await expect(page.getByTestId('content-table')).toBeVisible();
    await page.getByTestId('content-new-post').click();
    await expect(page.getByTestId('content-editor-form')).toBeVisible();

    // The slug auto-fills from the title (Alpine "slugify") until it is
    // touched. Re-fill until it lands: the first input event can fire before
    // Alpine has initialized the component, and nothing would set it after.
    const titleInput = page.getByTestId('content-title');
    await expect
      .poll(async () => {
        await titleInput.fill(title);
        return page.getByTestId('content-slug').inputValue();
      })
      .toBe(slug);

    // An explicit publish date before the frozen TEST_NOW clock keeps the
    // computed status badge deterministic instead of tracking wall time.
    await page.getByTestId('content-published-at').fill('2026-01-10T09:00');
    // data-testid names the MarkdownEditor root; the textarea inside it is
    // still the form value, so that is what gets filled.
    await page.getByTestId('content-body').locator('textarea').fill(`**Bold body** for ${title}.`);

    // The preview pane round-trips the markdown through the server
    // (hx-post /admin/content/preview), so a <strong> here proves the same
    // goldmark instance the save path uses rendered it.
    await expect(page.locator('#content-preview strong')).toHaveText('Bold body');

    await page.getByTestId('content-save').click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(page).toHaveURL(/\/admin\/content$/);

    // Newest match: a retried run can leave an older row with the same text.
    const rowFor = (text: string) =>
      page.locator('[data-testid^="content-row-"]').filter({ hasText: text }).last();
    const row = rowFor(title);
    await expect(row).toBeVisible();
    await expect(row.getByTestId('content-status')).toHaveText('Draft');

    // The id comes from the row we just created — never hardcoded.
    const rowTestID = (await row.getAttribute('data-testid')) ?? '';
    const id = rowTestID.replace('content-row-', '');
    expect(id).toMatch(/^\d+$/);

    // Publish, and wait for the badge to flip BEFORE navigating: going
    // straight to /blog races the in-flight POST, and the entry is only
    // public once the mutation landed and invalidated the CMS cache.
    await page.getByTestId(`content-publish-${id}`).click();
    await expect(
      page.getByTestId(`content-row-${id}`).getByTestId('content-status'),
    ).toHaveText('Live');

    await page.goto('/blog');
    const publicLink = page.getByRole('link', { name: title });
    await expect(publicLink).toBeVisible();
    await publicLink.click();
    await expect(page).toHaveURL(new RegExp(`/blog/${slug}$`));
    await expect(page.getByRole('heading', { name: title })).toBeVisible();
    await expect(page.locator('.prose strong')).toHaveText('Bold body');

    // Edit + save snapshots a second revision, so the panel now offers a
    // restore back to the original title.
    await page.goto(`/admin/content/${id}`);
    await page.getByTestId('content-title').fill(edited);
    await page.getByTestId('content-save').click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(rowFor(edited)).toBeVisible();

    await page.goto(`/admin/content/${id}`);
    const restore = page.locator('[data-testid^="content-restore-"]');
    // Two revisions: the newest is badged "current", so exactly one is restorable.
    await expect(restore).toHaveCount(1);
    await restore.click();
    // The older revision is still only offered, not applied.
    await expect(openDialog(page)).toBeVisible();
    await expect(page.getByTestId('content-title')).toHaveValue(edited);
    await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await expect(rowFor(title)).toBeVisible();
    await expect(rowFor(edited)).toHaveCount(0);

    // Delete every row this run created (delete-while-any, never a
    // precomputed count: each Navigate re-renders the whole table). Reload
    // before each delete so a click cannot land on a detached row.
    const stale = () =>
      page.locator('[data-testid^="content-row-"]').filter({ hasText: String(stamp) });
    for (let guard = 0; guard < 10; guard++) {
      await page.goto('/admin/content');
      if ((await stale().count()) === 0) break;
      const staleID = await stale().first().getAttribute('data-testid');
      await stale().first().getByRole('button', { name: 'Delete' }).click();
      // Row intact while the confirmation is open.
      await expect(page.locator(`[data-testid="${staleID}"]`)).toHaveCount(1);
      await openDialog(page).getByRole('button', { name: 'Confirm' }).click();
      await expect(page.locator(`[data-testid="${staleID}"]`)).toHaveCount(0);
    }
    await page.goto('/admin/content');
    await expect(stale()).toHaveCount(0);

    await page.goto('/blog');
    await expect(page.getByRole('link', { name: title })).toHaveCount(0);

    await context.close();
  });
});

test('support staff read the admin area without the controls', async ({ browser }) => {
  const context = await loginAs(browser, 'support');
  const page = await context.newPage();

  // The dashboards are readable…
  for (const path of ['/admin', '/admin/users', '/admin/flags', '/admin/audit']) {
    await page.goto(path);
    await expect(page.getByRole('heading').first()).toBeVisible();
  }

  // …the data is there…
  await page.goto('/admin/flags');
  await expect(page.getByTestId('flags-table')).toBeVisible();

  // …but nothing that mutates is offered.
  await expect(page.getByTestId('flag-create-form')).toHaveCount(0);
  await page.goto('/admin/users');
  await expect(page.getByTestId('admin-impersonate')).toHaveCount(0);
  await expect(page.getByTestId('admin-disable-toggle')).toHaveCount(0);
  await expect(page.getByTestId('admin-readonly').first()).toBeVisible();

  // And the boundary is enforced server-side, not just hidden in the UI.
  const resp = await page.request.post('/admin/users/user_pro/disable');
  expect(resp.status()).toBe(403);

  await context.close();
});
