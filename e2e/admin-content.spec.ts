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
