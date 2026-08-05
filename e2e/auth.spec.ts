import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Auth flows against the DEV_AUTH_BYPASS synthetic tokens — every guard and
// middleware still executes.
test.describe('auth', () => {
  test('anonymous /app redirects to /login', async ({ page }) => {
    // Assert the guard's 303 directly (the browser would keep following the
    // dev-login chain in bypass mode).
    const response = await page.request.get('/app', { maxRedirects: 0 });
    expect(response.status()).toBe(303);
    expect(response.headers()['location']).toBe('/login');
  });

  test('free user lands on the dashboard with their org', async ({ browser }) => {
    const context = await loginAs(browser, 'free');
    const page = await context.newPage();
    await page.goto('/app');
    await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
    await expect(page.getByTestId('stat-card').first()).toBeVisible();
    await context.close();
  });

  test('user with zero orgs is sent to create one', async ({ browser }) => {
    const context = await loginAs(browser, 'noorg');
    const page = await context.newPage();
    // The 303 targets the portal's create-organization (an invited teammate
    // must never be told to found a competing org). The host is fake in e2e,
    // so assert on the attempted navigation, not a successful load.
    const request = page.waitForRequest((req) =>
      req.url().includes('accounts.example.test/create-organization'),
    );
    await page.goto('/app').catch(() => {});
    await request;
    await context.close();
  });

  test('member with no active org sees SelectOrg', async ({ browser }) => {
    const context = await loginAs(browser, 'noactive');
    const page = await context.newPage();
    await page.goto('/app');
    await expect(page.getByRole('heading', { name: 'Choose an organization' })).toBeVisible();
    await expect(page.getByTestId('select-org-button').first()).toBeVisible();
    await expect(page.getByText('Free Org')).toBeVisible();
    await context.close();
  });

  test('disabled user gets the 403 disabled page', async ({ browser }) => {
    const context = await loginAs(browser, 'disabled');
    const page = await context.newPage();
    const response = await page.goto('/app');
    expect(response?.status()).toBe(403);
    await expect(page.getByRole('heading', { name: 'Account disabled' })).toBeVisible();
    await context.close();
  });

  test('app nav swaps only #content, leaving the shell and body-level portals alive', async ({ browser }) => {
    const context = await loginAs(browser, 'admin');
    const page = await context.newPage();
    await page.goto('/app');
    await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();

    // Tag the live mount roots, and append a sentinel directly to <body> — that
    // is exactly where clerk-js renders its dropdown menus (a portal that is a
    // SIBLING of the mount root, so no per-element attribute can protect it).
    // Any swap or morph of <body> deletes it and the dropdowns go dead.
    await page.evaluate(() => {
      document.getElementById('user-button')?.setAttribute('data-live', 'ub');
      document.getElementById('org-switcher')?.setAttribute('data-live', 'os');
      const portal = document.createElement('div');
      portal.id = 'body-portal-sentinel';
      document.body.appendChild(portal);
    });

    // Alpine in the persistent shell must also keep working: drive the theme
    // toggle before navigating so we can prove its bindings survive.
    const themeToggle = page.getByTestId('theme-toggle');
    await themeToggle.click();
    await expect.poll(() => page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(true);

    await page.getByRole('link', { name: 'Projects', exact: true }).click();
    await expect(page).toHaveURL(/\/app\/projects$/);
    await expect(page).toHaveTitle(/^Projects/);
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();

    // The shell was never a swap target: same mount nodes, and the body-level
    // portal sentinel is still attached.
    await expect.poll(() =>
      page.evaluate(() => [
        document.getElementById('user-button')?.getAttribute('data-live'),
        document.getElementById('org-switcher')?.getAttribute('data-live'),
        document.getElementById('body-portal-sentinel') ? 'portal' : null,
      ]),
    ).toEqual(['ub', 'os', 'portal']);

    // Alpine's bindings in the shell still react (exactly one theme icon shown,
    // and toggling still flips the class).
    await expect.poll(() =>
      page.evaluate(() =>
        Array.from(document.querySelectorAll('[data-testid="theme-toggle"] svg')).filter(
          (svg) => getComputedStyle(svg).display !== 'none',
        ).length,
      ),
    ).toBe(1);
    await themeToggle.click();
    await expect.poll(() => page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(false);

    // Nav state is synced client-side because the sidebar is never re-rendered.
    await expect(page.getByRole('link', { name: 'Projects', exact: true })).toHaveAttribute(
      'aria-current',
      'page',
    );
    await expect(page.getByRole('link', { name: 'Dashboard', exact: true })).not.toHaveAttribute(
      'aria-current',
      'page',
    );

    await page.getByRole('link', { name: 'Settings', exact: true }).click();
    await expect(page).toHaveURL(/\/app\/settings\/account$/);
    await expect(page.getByRole('link', { name: 'Billing' })).toBeVisible();
    await expect.poll(() =>
      page.evaluate(() => [
        document.getElementById('user-button')?.getAttribute('data-live'),
        document.getElementById('body-portal-sentinel') ? 'portal' : null,
      ]),
    ).toEqual(['ub', 'portal']);
    await expect(page.getByRole('link', { name: 'Settings', exact: true })).toHaveAttribute(
      'aria-current',
      'page',
    );

    // Back/Forward: htmx 4 re-fetches the URL and swaps it into the
    // hx-history-elt (#content). A full page must not end up nested inside it,
    // and the shell must come through unchanged.
    const shellShape = () =>
      page.evaluate(() => ({
        contents: document.querySelectorAll('#content').length,
        headings: document.querySelectorAll('h1').length,
        navLinks: document.querySelectorAll('[data-app-nav]').length,
        userButtons: document.querySelectorAll('#user-button').length,
        nested: /<html|<body|<!doctype/i.test(document.getElementById('content')?.innerHTML ?? ''),
      }));
    const beforeHistory = await shellShape();
    expect(beforeHistory).toEqual({
      contents: 1,
      headings: 1,
      navLinks: beforeHistory.navLinks,
      userButtons: 1,
      nested: false,
    });
    expect(beforeHistory.navLinks).toBeGreaterThan(3);

    await page.goBack();
    await expect(page).toHaveURL(/\/app\/projects$/);
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await expect.poll(shellShape).toEqual(beforeHistory);
    await expect(page.getByRole('link', { name: 'Projects', exact: true })).toHaveAttribute(
      'aria-current',
      'page',
    );

    await page.goForward();
    await expect(page).toHaveURL(/\/app\/settings\/account$/);
    await expect.poll(shellShape).toEqual(beforeHistory);
    await context.close();
  });

  test('Clerk recovers from a transient load failure without an unhandled rejection', async ({ page }) => {
    const pageErrors: string[] = [];
    page.on('pageerror', (error) => pageErrors.push(error.message));

    await page.route('**/__clerk-retry', (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: `
          <!doctype html>
          <html>
            <head>
              <meta name="clerk-publishable-key" content="pk_test_fixture">
              <script>
                window.__clerkLoadAttempts = 0;
                window.Clerk = {
                  user: { id: 'user_fixture' },
                  session: { id: 'session_fixture' },
                  load() {
                    window.__clerkLoadAttempts += 1;
                    if (window.__clerkLoadAttempts === 1) {
                      return Promise.reject(new Error('transient Clerk failure'));
                    }
                    return Promise.resolve(this);
                  },
                  mountUserButton(element) { element.textContent = 'USER_MOUNTED'; },
                  mountOrganizationSwitcher(element) { element.textContent = 'ORG_MOUNTED'; },
                  unmountUserButton() {},
                  unmountOrganizationSwitcher() {},
                };
              </script>
              <script src="/static/app.js"></script>
              <script defer src="/static/vendor/htmx.min.js"></script>
            </head>
            <body>
              <div id="org-switcher" hx-morph-skip></div>
              <div id="user-button" hx-morph-skip></div>
              <nav>
                <a href="/__clerk-retry-next" hx-boost="true" hx-target="body" hx-swap="innerMorph">Retry</a>
              </nav>
            </body>
          </html>`,
      }),
    );
    await page.route('**/__clerk-retry-next', (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: `
          <!doctype html>
          <html>
            <head>
              <title>Retry</title>
              <meta name="clerk-publishable-key" content="pk_test_fixture">
            </head>
            <body>
              <div id="org-switcher" hx-morph-skip></div>
              <div id="user-button" hx-morph-skip></div>
              <nav>
                <a href="/__clerk-retry-next" hx-boost="true" hx-target="body" hx-swap="innerMorph">Retry</a>
              </nav>
            </body>
          </html>`,
      }),
    );

    const mountedText = (id: string) =>
      page.evaluate((sel) => document.getElementById(sel)?.textContent ?? '', id);

    await page.goto('/__clerk-retry');
    // The transient rejection is retried asynchronously (one self-driven retry),
    // so wait for the second load attempt before asserting the mount landed.
    await page.waitForFunction(() => Reflect.get(window, '__clerkLoadAttempts') === 2);
    await expect.poll(() => mountedText('user-button')).toBe('USER_MOUNTED');
    await expect.poll(() => mountedText('org-switcher')).toBe('ORG_MOUNTED');
    expect(pageErrors).toEqual([]);

    // A boosted morph navigation must NOT re-load or re-mount Clerk: the mount
    // roots are hx-morph-skip, so the widgets survive untouched.
    await page.getByRole('link', { name: 'Retry' }).click();
    await expect(page).toHaveURL(/\/__clerk-retry-next$/);
    await expect.poll(() => mountedText('user-button')).toBe('USER_MOUNTED');
    await expect.poll(() => mountedText('org-switcher')).toBe('ORG_MOUNTED');
    await expect
      .poll(() => page.evaluate(() => Reflect.get(window, '__clerkLoadAttempts')))
      .toBe(2);
    expect(pageErrors).toEqual([]);
  });

  test('organization switching waits for Clerk and surfaces a failure', async ({ page }) => {
    const pageErrors: string[] = [];
    page.on('pageerror', (error) => pageErrors.push(error.message));

    await page.route('**/__clerk-select-org', (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: `
          <!doctype html>
          <html>
            <head>
              <meta name="clerk-publishable-key" content="pk_test_fixture">
              <script>
                window.__clerkState = { load: 0, setActive: 0, toast: null };
                window.Clerk = {
                  user: { id: 'user_fixture' },
                  session: { id: 'session_fixture' },
                  load() {
                    window.__clerkState.load += 1;
                    const deferred = Promise.withResolvers();
                    window.__finishClerkLoad = () => deferred.resolve(this);
                    return deferred.promise;
                  },
                  setActive() {
                    window.__clerkState.setActive += 1;
                    return Promise.reject(new Error('organization switch failed'));
                  },
                  mountUserButton() {},
                  mountOrganizationSwitcher() {},
                  unmountUserButton() {},
                  unmountOrganizationSwitcher() {},
                };
                document.addEventListener('toast', (event) => {
                  window.__clerkState.toast = event.detail;
                });
              </script>
              <script src="/static/app.js"></script>
              <script defer src="/static/vendor/alpine-csp.min.js"></script>
            </head>
            <body>
              <div x-data="selectOrg">
                <button type="button" @click="choose('org_fixture')">Choose organization</button>
              </div>
            </body>
          </html>`,
      }),
    );

    await page.goto('/__clerk-select-org');
    await page.getByRole('button', { name: 'Choose organization' }).click();
    expect(
      await page.evaluate(() => Reflect.get(window, '__clerkState').setActive),
    ).toBe(0);

    await page.evaluate(() => Reflect.get(window, '__finishClerkLoad')());
    await expect
      .poll(() => page.evaluate(() => Reflect.get(window, '__clerkState').setActive))
      .toBe(1);
    await expect
      .poll(() => page.evaluate(() => Reflect.get(window, '__clerkState').toast?.message))
      .toBe('Unable to switch organizations. Please try again.');
    expect(pageErrors).toEqual([]);
  });
});
