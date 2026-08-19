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
test('rotate signing secret', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  page.on('dialog', (dialog) => dialog.accept());
  await page.goto('/app/settings/webhooks');

  await page.getByTestId('webhook-url').fill('https://example.com/hooks/rotate');
  await page.getByTestId('webhook-create').click();
  const row = page.getByTestId('webhook-endpoint-row').filter({ hasText: 'hooks/rotate' });
  await expect(row).toHaveCount(1);

  await row.locator('[data-testid^="webhook-rotate-"]').click();
  const reveal = page.getByTestId('webhook-secret-reveal');
  await expect(reveal).toBeVisible();
  await expect(page.getByTestId('webhook-rotate-note')).toBeVisible();
  await expect(reveal.locator('code')).toContainText('whsec_');

  // Shown exactly once.
  await page.reload();
  await expect(page.getByTestId('webhook-secret-reveal')).toBeHidden();
  await expect(row).toHaveCount(1);

  await context.close();
});
