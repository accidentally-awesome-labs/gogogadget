import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Loading feedback. Delays are injected by the test with route interception,
// not by throttling the machine: the assertions are then about behaviour, not
// about how fast this laptop happens to be.

test('a slow navigation shows progress, and clears when it lands', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app');

  let release: () => void = () => {};
  const held = new Promise<void>((r) => (release = r));
  await page.route('**/app/projects', async (route) => {
    await held;
    await route.continue();
  });

  await page.getByRole('link', { name: 'Projects', exact: true }).click();

  // The document is flagged while the swap is in flight; CSS draws the bar
  // and dims the stale content from that flag.
  await expect(page.locator('html')).toHaveAttribute('data-navigating', '');
  await expect
    .poll(() => page.evaluate(() => parseFloat(getComputedStyle(document.getElementById('content')!).opacity)))
    .toBeLessThan(1);

  release();
  await expect(page).toHaveURL(/\/app\/projects$/);

  // …and it must clear, or every later page renders permanently dimmed.
  await expect(page.locator('html')).not.toHaveAttribute('data-navigating', '');
  await expect
    .poll(() => page.evaluate(() => parseFloat(getComputedStyle(document.getElementById('content')!).opacity)))
    .toBe(1);

  await context.close();
});

// The flag is cleared before the swap on purpose: an element inserted while
// it is still set paints dimmed from birth (CSS transitions do not apply to
// initial values), which flashes grey on every fast navigation.
test('a completed navigation renders at full opacity immediately', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app');

  const opacities: number[] = [];
  await page.getByRole('link', { name: 'Projects', exact: true }).click();
  for (let i = 0; i < 12; i++) {
    opacities.push(
      await page.evaluate(() => parseFloat(getComputedStyle(document.getElementById('content')!).opacity)),
    );
    await page.waitForTimeout(40);
  }
  await expect(page).toHaveURL(/\/app\/projects$/);
  expect(Math.min(...opacities), 'a local navigation must not flicker').toBe(1);

  await context.close();
});

// In-page updates are not navigations: a table search must not dim the page.
test('in-page swaps do not trigger the navigation indicator', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app/projects');

  await page.getByRole('searchbox').fill('alpha');
  await page.waitForTimeout(500);
  await expect(page.locator('html')).not.toHaveAttribute('data-navigating', '');

  await context.close();
});

test('the notification badge reserves its space while loading', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();

  let release: () => void = () => {};
  const held = new Promise<void>((r) => (release = r));
  await page.route('**/app/notifications/badge', async (route) => {
    await held;
    await route.continue();
  });

  await page.goto('/app');
  const skeleton = page.locator('#notif-badge .skeleton');
  await expect(skeleton).toBeVisible();
  const box = await skeleton.boundingBox();
  expect(box?.width, 'the placeholder occupies the space the count will take').toBeGreaterThan(0);

  release();
  await expect(skeleton).toHaveCount(0);

  await context.close();
});
