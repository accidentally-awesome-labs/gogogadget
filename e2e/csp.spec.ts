import { test, expect } from '@playwright/test';

// Every third-party byte this product ships is committed to the repository and
// served from this origin. These tests observe that in the browser: a CDN
// request or a CSP violation means the vendoring has been bypassed somewhere,
// and the manifest digests would then be checking files nobody loads.

// Pages worth checking individually: the gallery loads every lazily fetched
// engine, and the app shell is where the auth vendor and the SSE stream live.
const surfaces = ['/', '/pricing', '/dev/gallery', '/app/projects'];

// Hosts that are only ever identifiers, never fetched. An SVG namespace URI
// appears in every icon and resolves to no request.
const NON_FETCHING = ['www.w3.org'];

function isOffOrigin(url: string, origin: string): boolean {
  if (!/^https?:/i.test(url)) return false;
  if (url.startsWith(origin)) return false;
  return !NON_FETCHING.some((host) => url.includes(host));
}

test.describe('content security policy', () => {
  for (const path of surfaces) {
    test(`${path} makes no off-origin request`, async ({ page, baseURL }) => {
      const origin = new URL(baseURL!).origin;
      const offOrigin: string[] = [];
      const violations: string[] = [];

      // A CSP violation surfaces as a console error; the report is the only
      // signal for a blocked inline script, which no request event would show.
      page.on('console', (message) => {
        const text = message.text();
        if (/content security policy|refused to (load|execute|connect)/i.test(text)) {
          violations.push(text);
        }
      });
      page.on('request', (request) => {
        if (isOffOrigin(request.url(), origin)) offOrigin.push(request.url());
      });

      await page.goto(path);
      await page.waitForLoadState('networkidle');

      expect(offOrigin, 'off-origin requests').toEqual([]);
      expect(violations, 'CSP violations').toEqual([]);
    });
  }

  test('the policy forbids inline and off-origin script', async ({ request }) => {
    const response = await request.get('/');
    const policy = response.headers()['content-security-policy'];
    expect(policy, 'a page with no policy is a page with no protection').toBeTruthy();

    // The script directive specifically: 'self' with nothing widening it is
    // what makes the vendored files the only script that can run. style-src
    // does allow inline styles, which is a different decision and not this
    // test's business - asserting on the whole policy string would conflate
    // the two and fail for the wrong reason.
    const scriptSrc = policy.split(';').map((part) => part.trim())
      .find((part) => part.startsWith('script-src'));
    expect(scriptSrc).toBe("script-src 'self'");
    expect(policy).not.toContain('unsafe-eval');
    expect(policy).not.toContain('cdn.jsdelivr.net');
  });

  test('lazily loaded engines come from this origin with integrity', async ({ page, baseURL }) => {
    const origin = new URL(baseURL!).origin;
    await page.goto('/dev/gallery');
    await page.waitForLoadState('networkidle');

    // The gallery renders a chart, a date picker and a board, so all three
    // engines are loaded here - and each must be same-origin and pinned.
    const engines = await page.locator('script[data-ui-engine-src]').evaluateAll((nodes) =>
      nodes.map((node) => ({
        src: node.getAttribute('src') ?? '',
        integrity: node.getAttribute('integrity') ?? '',
      })),
    );
    expect(engines.length, 'the gallery should load its engines').toBeGreaterThan(0);
    for (const engine of engines) {
      expect(engine.src.startsWith('/') || engine.src.startsWith(origin)).toBe(true);
      expect(engine.integrity, `${engine.src} must be pinned`).toMatch(/^sha256-/);
    }
  });

  test('no script element points at another origin', async ({ page, baseURL }) => {
    const origin = new URL(baseURL!).origin;
    for (const path of surfaces) {
      await page.goto(path);
      const offOrigin = await page.locator('script[src]').evaluateAll(
        (nodes, args) =>
          nodes
            .map((node) => node.getAttribute('src') ?? '')
            .filter((src) => /^https?:/i.test(src) && !src.startsWith(args as string)),
        origin,
      );
      expect(offOrigin, `${path} script sources`).toEqual([]);
    }
  });
});
