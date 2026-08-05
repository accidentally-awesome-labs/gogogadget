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

  test('docs table of contents arrives and leaves with the page', async ({ page }) => {
    // The header renders its links twice (desktop + mobile menu) and the footer
    // repeats them, so scope nav clicks to the visible header link.
    const headerLink = (name: string) =>
      page.locator('header').getByRole('link', { name, exact: true }).first();
    const toc = page.getByRole('navigation', { name: 'Documentation' });
    const footer = page.locator('footer');

    await page.goto('/');
    await expect(toc).toHaveCount(0);
    await expect(footer).toHaveCount(1);

    // Navigation swaps only #content, so the docs sidebar has to live inside it.
    await headerLink('Docs').click();
    await expect(page).toHaveURL(/\/docs\//);
    await expect(toc).toBeVisible();
    await expect(footer).toHaveCount(1);

    // Leaving the docs section must take the sidebar with it.
    await headerLink('Pricing').click();
    await expect(page).toHaveURL(/\/pricing$/);
    await expect(page.getByRole('heading', { name: 'Free' })).toBeVisible();
    await expect(toc).toHaveCount(0);
    await expect(footer).toHaveCount(1);
    await expect(page.locator('main')).toHaveCount(1);
  });

  test('the Features anchor scrolls in place without a request', async ({ page }) => {
    await page.goto('/');

    // Boosting an in-page anchor makes htmx fetch the page and repaint #content
    // at the top before scrolling — a flash of the hero. It must stay unboosted,
    // which means no request at all fires.
    const features = page.locator('header').getByRole('link', { name: 'Features', exact: true }).first();
    await expect(features).not.toHaveAttribute('hx-boost', 'true');

    let requests = 0;
    page.on('request', (request) => {
      if (request.headers()['hx-request']) requests += 1;
    });
    await features.click();

    await expect(page).toHaveURL(/#features$/);
    await expect.poll(() => page.evaluate(() => window.scrollY > 200)).toBe(true);
    expect(requests).toBe(0);
  });

  test('navigation lands at the top of the new page', async ({ page }) => {
    await page.goto('/docs/index');
    await page.evaluate(() => window.scrollTo(0, 900));
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(500);

    // A boosted link only swaps #content, so without an explicit show:top htmx
    // keeps the old scroll offset and drops you mid-page. htmx defaults only
    // boosted *forms* to scrolling, never links.
    await page.locator('aside').getByRole('link', { name: 'Frontend', exact: true }).click();
    await expect(page).toHaveURL(/\/docs\/frontend$/);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeLessThan(200);
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
