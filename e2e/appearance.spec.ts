import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

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
