import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Outbound webhooks: create endpoint → secret shown once → empty delivery log.
test('create endpoint, secret reveal, empty deliveries', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/settings/webhooks');

  await page.getByTestId('webhook-url').fill('https://example.com/hooks/ggg');
  await page.getByTestId('webhook-create').click();

  // Secret shown once.
  const reveal = page.getByTestId('webhook-secret-reveal');
  await expect(reveal).toBeVisible();
  await expect(reveal.locator('code')).toContainText('whsec_');

  // Endpoint row + empty deliveries table (scoped to THIS endpoint: other
  // specs in the file create their own).
  await expect(page.getByTestId('webhook-endpoint-row').filter({ hasText: 'hooks/ggg' })).toHaveCount(1);
  await expect(page.getByText('No deliveries yet.')).toBeVisible();

  // Reload: secret gone, endpoint stays.
  await page.reload();
  await expect(page.getByTestId('webhook-secret-reveal')).toBeHidden();
  await expect(page.getByTestId('webhook-endpoint-row').filter({ hasText: 'hooks/ggg' })).toHaveCount(1);


  await context.close();
});

// Rotation: new secret revealed once, previous kept verifying for the grace
// window (the note states it). Own endpoint so the specs stay independent.
//
// Rotation is gated by an in-page AlertDialog rather than the native confirm(),
// so there is no browser dialog to accept here; a leftover handler for one
// would quietly let a regression that restored confirm() pass.
test('rotate signing secret', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/settings/webhooks');

  await page.getByTestId('webhook-url').fill('https://example.com/hooks/rotate');
  await page.getByTestId('webhook-create').click();
  const row = page.getByTestId('webhook-endpoint-row').filter({ hasText: 'hooks/rotate' });
  await expect(row).toHaveCount(1);

  // The secret creation just minted, so the assertions below can prove the
  // trigger alone did NOT mint a second one.
  const reveal = page.getByTestId('webhook-secret-reveal');
  await expect(reveal.locator('code')).toContainText('whsec_');
  const createdSecret = await reveal.locator('code').innerText();

  await row.locator('[data-testid^="webhook-rotate-"]').click();
  const dialog = page.locator('dialog[data-ui="alert-dialog"][open]');
  await expect(dialog).toBeVisible();
  // Nothing has rotated yet: same secret on screen, no grace-window note.
  await expect(reveal.locator('code')).toHaveText(createdSecret);
  await expect(page.getByTestId('webhook-rotate-note')).toHaveCount(0);

  await dialog.getByRole('button', { name: 'Rotate secret', exact: true }).click();
  await expect(reveal).toBeVisible();
  await expect(page.getByTestId('webhook-rotate-note')).toBeVisible();
  await expect(reveal.locator('code')).toContainText('whsec_');
  await expect(reveal.locator('code')).not.toHaveText(createdSecret);

  // Shown exactly once.
  await page.reload();
  await expect(page.getByTestId('webhook-secret-reveal')).toBeHidden();
  await expect(row).toHaveCount(1);

  await context.close();
});
