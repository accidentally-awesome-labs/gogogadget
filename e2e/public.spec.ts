import { test, expect } from '@playwright/test';

// Public surface: marketing, pricing, blog, feeds, dark mode, 404.
test.describe('public pages', () => {
  test('landing renders the hero', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId('hero')).toBeVisible();
    await expect(page.getByRole('link', { name: 'Start building' })).toBeVisible();
  });

  test('pricing renders three plan cards', async ({ page }) => {
    await page.goto('/pricing');
    await expect(page.getByRole('heading', { name: 'Free' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Pro', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Team' })).toBeVisible();
  });

  test('blog index → post', async ({ page }) => {
    await page.goto('/blog');
    await page.getByRole('link', { name: 'Hello, GoGoGadget' }).click();
    await expect(page.getByRole('heading', { name: 'Hello, GoGoGadget' })).toBeVisible();
    await expect(page.getByText('The stack is deliberately small')).toBeVisible();
  });

  test('feeds and SEO endpoints respond', async ({ page }) => {
    const rss = await page.request.get('/rss.xml');
    expect(rss.status()).toBe(200);
    expect(await rss.text()).toContain('<rss version="2.0">');

    const sitemap = await page.request.get('/sitemap.xml');
    expect(sitemap.status()).toBe(200);
    expect(await sitemap.text()).toContain('/blog/hello-world');

    const robots = await page.request.get('/robots.txt');
    expect(robots.status()).toBe(200);
  });

  test('docs index redirects and renders', async ({ page }) => {
    await page.goto('/docs');
    await expect(page).toHaveURL(/\/docs\/index$/);
    await expect(page.getByTestId('docs-page')).toBeVisible();
  });

  test('dark mode toggle persists across reload', async ({ page }) => {
    await page.goto('/');
    const html = page.locator('html');
    await expect(html).not.toHaveClass(/dark/);
    await page.getByTestId('theme-toggle').first().click();
    await expect(html).toHaveClass(/dark/);
    await page.reload();
    await expect(html).toHaveClass(/dark/);
  });

  test('unknown URL renders the styled 404', async ({ page }) => {
    const response = await page.goto('/definitely-not-here');
    expect(response?.status()).toBe(404);
    await expect(page.getByRole('heading', { name: '404' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Back to home' })).toBeVisible();
  });
});
