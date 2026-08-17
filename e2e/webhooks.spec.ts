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

  // Endpoint row + empty deliveries table.
  await expect(page.getByTestId('webhook-endpoint-row')).toHaveCount(1);
  await expect(page.getByText('No deliveries yet.')).toBeVisible();

  // Reload: secret gone, endpoint stays.
  await page.reload();
  await expect(page.getByTestId('webhook-secret-reveal')).toBeHidden();
  await expect(page.getByTestId('webhook-endpoint-row')).toHaveCount(1);

  await context.close();
});
