import type { Browser, BrowserContext } from '@playwright/test';

// Spec files own disjoint users/orgs — no cross-file shared-row mutation, so
// the suite is parallel-safe.
export type TestUser = 'free' | 'pro' | 'admin' | 'disabled' | 'noorg' | 'noactive' | 'deleteme';

const tokens: Record<TestUser, string> = {
  free: 'e2e:user_free:org_free:org:member',
  pro: 'e2e:user_pro:org_pro:org:admin',
  admin: 'e2e:user_admin:org_free:org:admin',
  disabled: 'e2e:user_disabled:org_free:org:member',
  noorg: 'e2e:user_noorg::',
  noactive: 'e2e:user_noactive::',
  deleteme: 'e2e:user_deleteme:org_deleteme:org:admin',
};

export async function loginAs(browser: Browser, user: TestUser): Promise<BrowserContext> {
  const context = await browser.newContext();
  // Cookie URL must match the configured baseURL (host.docker.internal inside
  // the pinned visual container).
  const base = process.env.E2E_BASE_URL ?? 'http://localhost:18080';
  await context.addCookies([{ name: '__session', value: tokens[user], url: base }]);
  return context;
}
