import type { Browser, BrowserContext } from '@playwright/test';
import { personas, type PersonaId } from './generated/personas';

// The actor list is generated from module persona declarations, so a spec and
// a fixture cannot disagree about who an actor is. The session token is minted
// by the server, so nothing here can invent one.
export type TestUser = PersonaId;

// mintSession asks ggg/workflow/dev-session for this persona's cookie.
//
// This harness cannot build a session token, deliberately. The synthetic
// token's grammar belongs to whichever identity adapter the environment
// selected and is written in exactly one place, in Go. It used to be restated
// here too, by a generated cookie helper, with nothing holding the two
// spellings together: a grammar change in Go kept every Go test green and
// failed only in this job, as a bounce to /login with no diagnostic anywhere.
// The copy was not even faithful — the Go minter refuses a subject containing
// the grammar's own separator, the template did not.
//
// So the token is fetched. `context.request` shares the context's cookie jar,
// so the reply's Set-Cookie lands on every page this context later opens. The
// request URL must match the configured baseURL (host.docker.internal inside
// the pinned visual container).
//
// The refusal reaches here as well, which is the other half of the point. A
// project whose test environment selects a hosted identity adapter has no
// minter at all, and the route answers 503 naming the missing capability —
// reported below as that message rather than as a redirect nobody can read.
export async function mintSession(context: BrowserContext, user: TestUser): Promise<void> {
  const persona = personas.find((candidate) => candidate.id === user);
  if (!persona) throw new Error(`unknown persona ${user}`);
  const base = process.env.E2E_BASE_URL ?? 'http://localhost:18080';
  const query = new URLSearchParams({ user: persona.user, org: persona.org, role: persona.role });
  const response = await context.request.get(`${base}/dev/session?${query.toString()}`);
  if (response.status() !== 204) {
    throw new Error(
      `cannot mint a session for persona ${user}: GET /dev/session answered ` +
        `${response.status()} ${(await response.text()).trim()}`,
    );
  }
}

export async function loginAs(browser: Browser, user: TestUser): Promise<BrowserContext> {
  const context = await browser.newContext();
  await mintSession(context, user);
  return context;
}
