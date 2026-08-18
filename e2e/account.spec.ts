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

  test('account deletion with confirmed email', async ({ browser }) => {
    const context = await loginAs(browser, 'deleteme');
    const page = await context.newPage();
    page.on('dialog', (dialog) => dialog.accept());
    await page.goto('/app/settings/account');

    // Wrong email first: rejected, nothing happens.
    await page.locator('input[name="confirm_email"]').fill('wrong@example.com');
    await page.getByTestId('delete-account').click();
    await expect(page.getByTestId('delete-account-error')).toBeVisible();

    // Right email: hard redirect home with a flash toast.
    await page.locator('input[name="confirm_email"]').fill('deleteme@example.com');
    await page.getByTestId('delete-account').click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByTestId('toast').first()).toBeVisible();
    await context.close();
  });
});
