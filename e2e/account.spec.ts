import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// The deleteme persona is disposable: sessionLoad lazily re-upserts the
// mirror row on any request, so deleting the account and re-authing with the
// same cookie just recreates it — the spec is retry-safe either way.
test.describe('account', () => {
  test('data export downloads JSON with the user data', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app/settings/account');

    const downloadPromise = page.waitForEvent('download');
    await page.getByTestId('export-data').click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe('gogogadget-data-export.json');

    const stream = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const chunk of stream) chunks.push(chunk as Buffer);
    const body = JSON.parse(Buffer.concat(chunks).toString());
    expect(body.user.email).toBe('pro@gogogadget.dev');
    expect(body.memberships).toEqual(
      expect.arrayContaining([expect.objectContaining({ org_id: 'org_pro', role: 'org:admin' })]),
    );
    await context.close();
  });

  test('notification preferences persist across reload', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app/settings/notifications');

    const welcome = page.getByTestId('pref-welcome').locator('input');
    await expect(welcome).toBeChecked();
    await welcome.uncheck();
    await page.getByTestId('notification-prefs-form').getByRole('button').click();

    await expect(page.getByTestId('toast').first()).toBeVisible();
    await page.reload();
    await expect(page.getByTestId('pref-welcome').locator('input')).not.toBeChecked();
    await expect(page.getByTestId('pref-payment_failed').locator('input')).toBeChecked();

    // Restore default-on so re-runs start from the seeded state.
    await page.getByTestId('pref-welcome').locator('input').check();
    await page.getByTestId('notification-prefs-form').getByRole('button').click();
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  // The native confirm() is gone: the danger zone now gates deletion behind an
  // in-page AlertDialog, so there is no browser dialog to accept and a handler
  // for one would only hide a regression that resurrected it.
  test('account deletion with confirmed email', async ({ browser }) => {
    const context = await loginAs(browser, 'deleteme');
    const page = await context.newPage();
    await page.goto('/app/settings/account');
    const dialog = page.locator('dialog[data-ui="alert-dialog"][open]');

    // Wrong email first: rejected, nothing happens.
    await page.locator('input[name="confirm_email"]').fill('wrong@example.com');
    await page.getByTestId('delete-account').click();
    // The trigger opens the dialog and issues NOTHING. Asserting the absence of
    // the server's answer here is the whole point: a ConfirmAction whose
    // trigger fired the request would pass a test that only added a second
    // click, and the confirmation would be decoration.
    await expect(dialog).toBeVisible();
    await expect(page.getByTestId('delete-account-error')).toHaveCount(0);

    await dialog.getByRole('button', { name: 'Yes, delete my account' }).click();
    await expect(page.getByTestId('delete-account-error')).toBeVisible();

    // Right email: hard redirect home with a flash toast.
    await page.locator('input[name="confirm_email"]').fill('deleteme@example.com');
    await page.getByTestId('delete-account').click();
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Yes, delete my account' }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });

  // A confirmation nobody can decline is theatre. Declining must close the
  // dialog and issue no request at all.
  //
  // The pro persona with a deliberately wrong address: if Cancel ever did fire
  // the request, the server answers 422 rather than deleting a live account, so
  // this test cannot destroy a fixture the rest of the suite depends on - and
  // the 422 fragment is exactly what the assertion below would catch.
  test('account deletion can be declined', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    const deleteRequests: string[] = [];
    page.on('request', (request) => {
      if (request.url().includes('/app/settings/account/delete')) deleteRequests.push(request.method());
    });
    await page.goto('/app/settings/account');

    await page.locator('input[name="confirm_email"]').fill('wrong@example.com');
    await page.getByTestId('delete-account').click();
    const dialog = page.locator('dialog[data-ui="alert-dialog"][open]');
    await expect(dialog).toBeVisible();

    await dialog.getByRole('button', { name: 'Keep my account' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator('#danger-zone')).toBeVisible();
    await expect(page.getByTestId('delete-account-error')).toHaveCount(0);
    expect(deleteRequests, 'cancel must not reach the server').toEqual([]);
    await context.close();
  });
});

test('appearance preferences follow the account to a new device', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/settings/account');

  // Locator assertions, not page.evaluate: changing either preference is a
  // HARD redirect (the theme class and lang live on <html>, which boosted
  // navigation never re-renders), and an evaluate races that navigation.
  await page.getByTestId('theme-dark').click();
  await expect(page.locator('html')).toHaveClass(/dark/);
  await page.getByTestId('locale-pref-es').click();
  await expect(page.locator('html')).toHaveAttribute('lang', /^es/);

  // A different browser: no localStorage, no preference cookies — only the
  // session. Both choices must already be applied on the first response.
  const fresh = await loginAs(browser, 'pro');
  const freshPage = await fresh.newPage();
  await freshPage.goto('/app');
  await expect(freshPage.locator('html')).toHaveClass(/dark/);
  await expect(freshPage.locator('html')).toHaveAttribute('lang', /^es/);

  // Put it back so the rest of the suite sees the default shell.
  await freshPage.goto('/app/settings/account');
  await freshPage.getByTestId('locale-pref-auto').click();
  await expect(freshPage.locator('html')).toHaveAttribute('lang', /^en/);
  await freshPage.getByTestId('theme-system').click();
  await expect(freshPage.locator('html')).not.toHaveClass(/dark/);

  await fresh.close();
  await context.close();
});
