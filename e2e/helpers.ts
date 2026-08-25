import type { Browser, BrowserContext } from '@playwright/test';
import { sessionFor, type PersonaId } from './generated/personas';

// The actor list is generated from module persona declarations; sessionFor
// builds the cookie, so a spec and a fixture cannot disagree about who an
// actor is. Spec files own disjoint users/orgs — no cross-file shared-row
// mutation, so the suite is parallel-safe.
export type TestUser = PersonaId;

export async function loginAs(browser: Browser, user: TestUser): Promise<BrowserContext> {
  const context = await browser.newContext();
  // Cookie URL must match the configured baseURL (host.docker.internal inside
  // the pinned visual container).
  const base = process.env.E2E_BASE_URL ?? 'http://localhost:18080';
  await context.addCookies([{ name: '__session', value: sessionFor(user), url: base }]);
  return context;
}
