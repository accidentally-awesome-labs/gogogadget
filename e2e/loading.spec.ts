import { test, expect } from '@playwright/test';
import { loginAs } from './helpers';

// Loading feedback. Delays are injected by the test with route interception,
// not by throttling the machine: the assertions are then about behaviour, not
// about how fast this laptop happens to be.

// The channel the in-page MutationObserver below reports through. It is a
// window property rather than a return value because the observation has to
// happen inside the page, at a moment between two DOM operations that no
// Playwright call can land in.
declare global {
  interface Window {
    __contentBirths?: Array<{ navigating: boolean; opacity: number }>;
  }
}

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
// initial values), which flashes grey on every navigation.
//
// This used to sample #content's opacity twelve times at 40ms intervals after
// clicking a fast link and assert the minimum was 1. That is not the ordering
// property; it is "this navigation completed inside the 150ms transition
// delay on the machine running the suite". And it was INVERTED. On a quick
// machine the dim never engages at all, so the minimum is 1 whether the flag
// is cleared before the swap or after it and the test passes vacuously; on a
// loaded machine the dim engages legitimately and the test fails. It had
// power only in the case where it went red, which is why it was observed
// passing on retry.
//
// So the response is HELD, which makes the dim certain rather than hoped for,
// and the assertion is the ordering itself: at the instant htmx inserts the
// new #content, the flag must already be gone. No wall clock is involved.
test('a completed navigation renders at full opacity immediately', async ({ browser }) => {
  const context = await loginAs(browser, 'pro');
  const page = await context.newPage();
  await page.goto('/app');

  // Recorded from inside the page, because the moment being asserted on is
  // between two DOM operations and no sample taken from Playwright can land
  // in it. A MutationObserver callback runs before the browser paints, and
  // reading getComputedStyle there forces the style resolution that paint
  // would use — so this is the opacity the new element WOULD appear with.
  //
  // NavSwap is `outerHTML`, so the element carrying id="content" is itself
  // replaced and the insertion is an addedNode rather than a child change.
  await page.evaluate(() => {
    window.__contentBirths = [];
    new MutationObserver((records) => {
      for (const record of records) {
        for (const node of Array.from(record.addedNodes)) {
          if (!(node instanceof HTMLElement) || node.id !== 'content') continue;
          window.__contentBirths!.push({
            navigating: document.documentElement.hasAttribute('data-navigating'),
            opacity: parseFloat(getComputedStyle(node).opacity),
          });
        }
      }
    }).observe(document.body, { childList: true, subtree: true });
  });

  let release: () => void = () => {};
  const held = new Promise<void>((r) => (release = r));
  await page.route('**/app/projects', async (route) => {
    await held;
    await route.continue();
  });

  await page.getByRole('link', { name: 'Projects', exact: true }).click();

  // The flash this test forbids is only possible once the dim has actually
  // engaged, so prove it did before releasing. This is what the old form
  // could not arrange and what made it pass for the wrong reason.
  await expect(page.locator('html')).toHaveAttribute('data-navigating', '');
  await expect
    .poll(() => page.evaluate(() => parseFloat(getComputedStyle(document.getElementById('content')!).opacity)))
    .toBeLessThan(1);

  release();
  await expect(page).toHaveURL(/\/app\/projects$/);

  const births = await page.evaluate(() => window.__contentBirths ?? []);
  expect(births.length, 'htmx never replaced #content, so nothing was observed').toBeGreaterThan(0);
  for (const birth of births) {
    expect(birth.navigating, 'the new #content was inserted while data-navigating was still set').toBe(false);
    expect(birth.opacity, 'the new #content would have painted dimmed from birth').toBe(1);
  }

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
