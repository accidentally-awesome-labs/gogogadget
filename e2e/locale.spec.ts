import { test, expect } from '@playwright/test';

// Locale switching: footer switcher → Spanish shell, cookie persistence,
// ?lang= one-shot override, and back to English.
test('footer switcher renders Spanish and persists', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByTestId('hero')).toContainText('Ship your SaaS');

  await page.getByTestId('locale-switcher').click();
  await page.getByTestId('locale-option-es').click();
  await page.waitForURL('/');

  await expect(page.getByTestId('hero')).toContainText('Lanza tu SaaS');
  await expect(page.locator('html')).toHaveAttribute('lang', 'es');

  // Cookie persists across a fresh navigation.
  await page.goto('/pricing');
  await expect(page.getByRole('heading', { name: 'Precios' })).toBeVisible();

  // Back to English via the switcher (returnTo brings us back).
  await page.getByTestId('locale-switcher').click();
  await page.getByTestId('locale-option-en').click();
  await page.waitForURL('/pricing');
  await expect(page.getByRole('heading', { name: 'Pricing' })).toBeVisible();
});

test('?lang=es renders Spanish for the session', async ({ page }) => {
  await page.goto('/?lang=es');
  await expect(page.getByTestId('hero')).toContainText('Lanza tu SaaS');
  // No cookie was written — a plain navigation is English again.
  await page.goto('/');
  await expect(page.getByTestId('hero')).toContainText('Ship your SaaS');
});
