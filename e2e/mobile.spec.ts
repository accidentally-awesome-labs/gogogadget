import { test, expect, devices } from '@playwright/test';
import { loginAs } from './helpers';

// Mobile app navigation: the sidebar is md:flex-only, so below md the ONLY
// way in is the topbar drawer. 375×812 = iPhone-class viewport.
test.use({ ...devices['iPhone 12'] });

test.describe('mobile nav', () => {
  test('drawer navigates without touching the shell', async ({ browser }) => {
    const context = await loginAs(browser, 'pro');
    const page = await context.newPage();
    await page.goto('/app');

    // Hamburger visible on mobile, drawer hidden until toggled.
    const toggle = page.getByTestId('mobile-nav-toggle');
    await expect(toggle).toBeVisible();
    await expect(page.getByTestId('mobile-nav-toggle')).toHaveAttribute('aria-expanded', 'false');

    await toggle.click();
    await expect(page.getByTestId('mobile-nav-toggle')).toHaveAttribute('aria-expanded', 'true');
    // :visible scopes to the drawer — the desktop sidebar holds the same
    // links but is hidden at this viewport.
    const drawerProjects = page.locator('[data-app-nav]:visible').filter({ hasText: 'Projects' }).first();
    await expect(drawerProjects).toBeVisible();

    // Sentinel on <body> proves the nav click swapped ONLY #content: a
    // body-level node appended before navigation must survive (auth.spec
    // pattern — clerk-js portals and Alpine roots live there).
    await page.evaluate(() => {
      const sentinel = document.createElement('div');
      sentinel.id = 'mobile-body-sentinel';
      document.body.appendChild(sentinel);
    });

    await drawerProjects.click();
    await expect(page).toHaveURL(/\/app\/projects$/);
    // body sentinel still attached proves a #content-only swap
    await expect(page.locator('#mobile-body-sentinel')).toBeAttached();

    // Drawer collapsed after navigation.
    await expect(page.getByTestId('mobile-nav-toggle')).toHaveAttribute('aria-expanded', 'false');
    await context.close();
  });
});
