import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test, expect, type Browser, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { loginAs, type TestUser } from './helpers';
import { surfaces } from './generated/surfaces';

// a11y.spec.ts scans surfaces as the server first renders them. A page-load
// scan can never see a dialog's contents, a menu panel, a selected tab panel or
// an open calendar, so every overlay and every progressively enhanced widget
// ships with its most complex state unscanned — exactly the states where the
// roles, names and focus wiring are applied by script rather than rendered.
// This file drives each one into that state first, then scans.
//
// The impact floor and the minor-finding annotation are copied from
// a11y.spec.ts rather than imported: that module registers its own tests at
// import time, so importing it would clone all 84 of them into this file.
const FAILING_IMPACTS: Record<string, true> = { moderate: true, serious: true, critical: true };

async function scan(page: Page, label?: string) {
  const results = await new AxeBuilder({ page }).analyze();
  const minor = results.violations.filter((v) => v.impact === 'minor');
  if (minor.length > 0) {
    test.info().annotations.push({
      type: 'axe-minor',
      description: `${label ?? 'state'}: ${minor.map((v) => `${v.id} (${v.nodes.length})`).join(', ')}`,
    });
  }
  const violations = results.violations.filter((v) => FAILING_IMPACTS[v.impact ?? '']);
  expect(violations, `${label ?? 'state'}\n${JSON.stringify(violations, null, 2)}`).toEqual([]);
}

// Asset and console activity per page, recorded from creation so a failure can
// say WHY an engine never arrived. A bare "waitForFunction timed out" cannot
// distinguish a slow fetch from a failed request from a loader that cached a
// rejection and will never retry — and that is the difference between machine
// contention and a product bug. Keyed on the page object, so it cannot outlive
// the context it describes.
const assetNotes = new WeakMap<Page, string[]>();

async function open(
  browser: Browser,
  persona: string,
  theme: 'light' | 'dark',
): Promise<{ page: Page; close: () => Promise<void> }> {
  const context = persona
    ? await loginAs(browser, persona as TestUser)
    : await browser.newContext();
  const page = await context.newPage();
  const notes: string[] = [];
  assetNotes.set(page, notes);
  page.on('requestfailed', (request) => {
    if (request.url().includes('/static/')) {
      notes.push(`requestfailed ${request.url()} ${request.failure()?.errorText ?? ''}`);
    }
  });
  page.on('response', (response) => {
    if (response.url().includes('/static/') && !response.ok()) {
      notes.push(`response ${response.status()} ${response.url()}`);
    }
  });
  page.on('console', (message) => {
    if (message.type() === 'error') notes.push(`console ${message.text()}`);
  });
  page.on('pageerror', (error) => notes.push(`pageerror ${error.message}`));
  if (theme === 'dark') {
    await page.addInitScript(() => localStorage.setItem('theme', 'dark'));
    await page.emulateMedia({ colorScheme: 'dark' });
  }
  return { page, close: () => context.close() };
}

const THEMES = ['light', 'dark'] as const;

// --- interaction states ----------------------------------------------------

// /dev/gallery is the only surface that renders a live instance of all 172
// components. The per-component reference pages under /dev/gallery/{family}/
// are documentation — signature, guidance, keyboard contract, installation —
// and render no instance at all, so an interaction state cannot be reached
// there. Verified by loading them: their only data-ui roots are the page
// chrome (page-header, description-list, code).
const GALLERY = '/dev/gallery';

// Every keyboard model, roving tabindex and combobox role in the catalog is
// applied by a controller on alpine:init, not rendered by the server. uiTree
// sets role="tree" in its init, so waiting for it proves Alpine has booted and
// walked the tree — without it a scan measures the unenhanced markup and would
// pass for the wrong reason.
async function galleryReady(page: Page) {
  await page.goto(GALLERY);
  await expect(page.locator('[data-ui="tree"][role="tree"]')).toBeAttached();
}

interface StateCase {
  id: string;
  desktopOnly?: boolean;
  // How many times the case scans. One axe pass over the 172-component gallery
  // is seconds of work, so a case that walks a widget's states needs a budget
  // proportional to its passes or it times out on a loaded machine while
  // everything it asserts is fine.
  scans?: number;
  // Whether reaching the state requires a lazily fetched vendor bundle. That
  // fetch queues behind the gallery's own two dozen scripts, so the case needs
  // an allowance on top of its scan budget; without it the enclosing test
  // expires mid-fetch on a busy machine and reports a widget defect that is
  // really contention.
  awaitsVendor?: boolean;
  run: (page: Page) => Promise<void>;
}

const interactionCases: StateCase[] = [
  // Overlays are located by the id the trigger opens, not by
  // `[data-ui="…"][open]`. /dev/gallery renders three alert-dialogs and two
  // drawers, so a state-based locator proves only that SOME overlay of that
  // family is open — never that it is the one this case opened — and it
  // resolves to nothing rather than failing usefully when the click is lost.
  // The data-ui assertion keeps the fixture honest: rename the component that
  // id belongs to and this case says so instead of quietly testing something
  // else.
  {
    id: 'dialog open',
    run: async (page) => {
      await page.getByRole('button', { name: 'Open dialog', exact: true }).click();
      const dialog = page.locator('#gallery-dialog');
      await expect(dialog).toHaveAttribute('data-ui', 'dialog');
      await expect(dialog).toBeVisible();
      await scan(page, 'dialog open');
    },
  },
  {
    id: 'alert-dialog open',
    run: async (page) => {
      await page.getByRole('button', { name: 'Confirm delete', exact: true }).click();
      const dialog = page.locator('#gallery-alert');
      await expect(dialog).toHaveAttribute('data-ui', 'alert-dialog');
      await expect(dialog).toBeVisible();
      // role="alertdialog" only helps if the consequence is actually wired as
      // the description, which is a scan-visible property of the open state.
      await expect(dialog).toHaveAccessibleDescription(/cannot be undone/i);
      await scan(page, 'alert-dialog open');
    },
  },
  {
    id: 'drawer open',
    run: async (page) => {
      await page.getByRole('button', { name: 'Open drawer', exact: true }).click();
      const drawer = page.locator('#gallery-drawer');
      await expect(drawer).toHaveAttribute('data-ui', 'drawer');
      await expect(drawer).toBeVisible();
      await scan(page, 'drawer open');
    },
  },
  {
    id: 'dropdown-menu open',
    run: async (page) => {
      // Every name here is matched exactly. getByRole's name option is a
      // substring match by default, so a control that merely gains a longer
      // name containing one of these — "File actions", "Details panel" —
      // silently makes the locator ambiguous and trips strict mode in a run
      // nobody connected to that change. It already happened once here: the
      // page's file input also exposes a button role whose name contains
      // "File".
      const trigger = page.getByRole('button', { name: 'File', exact: true });
      await trigger.click();
      await expect(trigger).toHaveAttribute('aria-expanded', 'true');
      await expect(page.locator('[data-ui="dropdown-menu"] [data-ui-menu-panel]').first()).toBeVisible();
      await scan(page, 'dropdown-menu open');
    },
  },
  {
    id: 'context-menu open',
    run: async (page) => {
      // Right-click is the shortcut; the panel it opens is the same
      // DropdownMenu the visible trigger opens, and it is the panel that has
      // never been scanned.
      await page.locator('[data-ui="context-menu"] [data-ui-context-region]').click({ button: 'right' });
      await expect(page.locator('[data-ui="context-menu"] [data-ui-menu-panel]')).toBeVisible();
      await scan(page, 'context-menu open');
    },
  },
  {
    id: 'popover open',
    run: async (page) => {
      const trigger = page.getByRole('button', { name: 'Details', exact: true });
      await trigger.click();
      await expect(trigger).toHaveAttribute('aria-expanded', 'true');
      await expect(page.locator('[data-ui="popover"] [data-ui-menu-panel]')).toBeVisible();
      await scan(page, 'popover open');
    },
  },
  {
    id: 'hover-card open',
    // One scan per instance; the gallery renders two.
    scans: 2,
    run: async (page) => {
      // Every instance, one at a time. The gallery deliberately renders two
      // hover cards because a controller that keeps its trigger or panel on the
      // shared Alpine component object instead of a per-element closure opens
      // the wrong instance's panel — and a page with one instance cannot show
      // that, which is why it survived until a second one existed.
      const cards = page.locator('[data-ui="hover-card"]');
      const count = await cards.count();
      expect(count, 'no hover card renders here').toBeGreaterThan(0);
      for (let i = 0; i < count; i++) {
        const card = cards.nth(i);
        // Focus, not hover: hover is absent on touch and unreachable by
        // keyboard, so the focus path is the one that has to be correct — and
        // it is the one that works under both projects.
        await card.locator('[data-ui-hovercard-trigger]').focus();
        await expect(card.locator('[data-ui-hovercard-panel]')).toBeVisible();
        // Exactly one panel open in the whole page. Cross-wiring shows up as a
        // second visible panel, never as a missing one, so asserting only that
        // this card opened would pass while another card opened too.
        await expect(page.locator('[data-ui="hover-card"] [data-ui-hovercard-panel]:visible')).toHaveCount(1);
        await scan(page, `hover-card ${i + 1} of ${count} open`);
      }
    },
  },
  {
    id: 'tooltip visible',
    run: async (page) => {
      // The tooltip is always in the DOM at opacity 0, so a page-load scan
      // reads its contrast against nothing. Focusing the trigger makes
      // group-focus-within paint it, which is when the contrast is real.
      const trigger = page.getByRole('button', { name: 'Hover me', exact: true });
      await trigger.focus();
      const tip = page.locator('[data-ui="tooltip"] [role="tooltip"]');
      await expect(async () => {
        expect(await tip.evaluate((el) => getComputedStyle(el).opacity)).toBe('1');
      }).toPass();
      await scan(page, 'tooltip visible');
    },
  },
  {
    id: 'command-palette open via trigger',
    run: async (page) => {
      await page.locator('[data-command-open]').click();
      await expect(page.locator('#gallery-command')).toBeVisible();
      // The combobox/listbox roles are applied by the controller, so they only
      // exist once it has run — the state a scan has to see.
      await expect(page.locator('[data-command-input]')).toHaveRole('combobox');
      await scan(page, 'command-palette open via trigger');
    },
  },
  {
    id: 'command-palette open via Ctrl+K',
    run: async (page) => {
      // The declared shortcut is a second, independent entry point: it is a
      // document-level handler, so it can regress while the trigger still
      // works.
      await page.keyboard.press('Control+k');
      await expect(page.locator('#gallery-command')).toBeVisible();
      await expect(page.locator('[data-command-input]')).toBeFocused();
      await scan(page, 'command-palette open via Ctrl+K');
    },
  },
  {
    id: 'tab-panels each tab selected',
    // One scan per tab; the fixture renders three.
    scans: 3,
    run: async (page) => {
      const tabs = page.locator('[data-ui="tab-panels"] [role="tab"]');
      const count = await tabs.count();
      expect(count, 'the tab widget renders no tabs').toBeGreaterThan(1);
      // Every panel but the first is hidden at load, so a page-load scan only
      // ever sees one third of this widget.
      for (let i = 0; i < count; i++) {
        const tab = tabs.nth(i);
        await tab.click();
        await expect(tab).toHaveAttribute('aria-selected', 'true');
        const panelId = await tab.getAttribute('aria-controls');
        await expect(page.locator(`#${panelId}`)).toBeVisible();
        await scan(page, `tab-panels tab ${i + 1} of ${count} selected`);
      }
    },
  },
  {
    id: 'combobox focused with a filter typed',
    run: async (page) => {
      // The datalist dropdown itself is browser chrome, not DOM: there is no
      // state to open and nothing for axe to see inside it. What is scannable
      // is the field once it is focused and holds a value, which is when its
      // description and invalid-value wiring have to still hold.
      const combobox = page.locator('input[data-ui="combobox"]');
      await expect(combobox).toHaveAccessibleName('Region');
      await combobox.fill('eu');
      await expect(combobox).toBeFocused();
      await scan(page, 'combobox focused with a filter typed');
    },
  },
  {
    id: 'date-picker calendar open',
    awaitsVendor: true,
    run: async (page) => {
      // The calendar and its whole shadow tree are built by the adapter, so a
      // page-load scan cannot reach them: at load the element does not exist.
      // This is the one case gated on a lazily fetched vendor bundle: the
      // trigger is rendered hidden and the adapter un-hides it only after
      // Cally's custom elements are defined, because a trigger that opened
      // nothing would be a dead control. Waiting on the definition rather than
      // on the button is waiting for the actual precondition — the un-hide
      // happens in the same handler — and the longer budget covers the network
      // fetch, not a race.
      const picker = page.locator('[data-ui="date-picker"]').filter({ has: page.locator('#starts_on') });
      try {
        await page.waitForFunction(() => window.customElements.get('calendar-date') !== undefined, null, {
          timeout: 20_000,
        });
      } catch (error) {
        // Say which of the three it was rather than leaving a bare timeout. The
        // loader keys one promise per engine and never retries a rejection, so
        // a single failed request disables the widget for the whole page load —
        // that is a product bug and must not read the same as a slow fetch.
        const notes = assetNotes.get(page) ?? [];
        throw new Error(
          `the cally engine never defined <calendar-date> within 20s. Recorded asset/console activity:\n${
            notes.join('\n') || '(none — no request failed and nothing errored, so the fetch was merely slow)'
          }`,
          { cause: error },
        );
      }
      const trigger = picker.getByRole('button', { name: 'Open calendar', exact: true });
      await expect(trigger).toBeVisible();
      await trigger.click();
      await expect(trigger).toHaveAttribute('aria-expanded', 'true');
      await expect(picker.locator('calendar-date')).toBeVisible();
      await scan(page, 'date-picker calendar open');
    },
  },
  {
    id: 'tree branch expanded and a deep node focused',
    run: async (page) => {
      const branch = page.locator('[data-tree-node="n-registry"] > details');
      await expect(branch).not.toHaveAttribute('open', '');
      const summary = branch.locator('> summary');
      await summary.focus();
      // ArrowRight on a closed branch is the declared contract, so this
      // exercises the same path a keyboard user takes to reveal the rows.
      await page.keyboard.press('ArrowRight');
      await expect(branch).toHaveAttribute('open', '');
      // A level-3 row inside two open branches is where a roving tabindex most
      // easily strands focus on a row the user cannot see.
      const deep = page.locator('[data-tree-node="n-templates"] [data-tree-item]');
      await deep.focus();
      await expect(deep).toBeFocused();
      await scan(page, 'tree branch expanded and a deep node focused');
    },
  },
  {
    id: 'data-grid cell focused',
    run: async (page) => {
      const cell = page.locator('[data-grid-table] tbody td').first();
      await cell.focus();
      await expect(cell).toBeFocused();
      await scan(page, 'data-grid cell focused');
    },
  },
  {
    id: 'data-grid column hidden',
    run: async (page) => {
      // The picker is rendered hidden because hiding a column does nothing
      // without the controller; its visibility means the controller is live.
      const picker = page.locator('[data-ui="column-picker"]');
      await expect(picker).toBeVisible();
      await picker.locator('summary').click();
      await picker.getByRole('checkbox', { name: 'Region', exact: true }).uncheck();
      // Hiding a column rewrites every row's cells and recomputes the grid's
      // single tab stop, so the table's header/cell association is being
      // rebuilt in the browser rather than rendered by the server.
      await expect(page.locator('[data-grid-table] th[data-grid-column="region"]')).toBeHidden();
      await scan(page, 'data-grid column hidden');
    },
  },
  {
    id: 'carousel scrolled to a middle slide',
    run: async (page) => {
      const track = page.locator('[data-ui="carousel"] [data-carousel-track]');
      const slides = await track.evaluate((el) => {
        const all = Array.from(el.querySelectorAll<HTMLElement>('[data-carousel-slide]'));
        const target = all[Math.floor(all.length / 2)];
        el.scrollLeft = target.offsetLeft - (el as HTMLElement).offsetLeft;
        return all.length;
      });
      // Which slide reports as current depends on the width available to the
      // scroller: when every slide fits, scrolling to the middle leaves the
      // scroller at the end of its range and the contract names the last slide
      // — nearest-the-left-edge would otherwise report the same slide forever.
      // What must hold either way is that the position is reported exactly
      // once, because aria-current is the only report a screen reader gets;
      // the filled dot is invisible to it.
      expect(slides).toBeGreaterThan(2);
      await expect(page.locator('[data-carousel-dot][aria-current="true"]')).toHaveCount(1);
      await scan(page, 'carousel scrolled to a middle slide');
    },
  },
  {
    id: 'panel-group handle focused after resizing',
    desktopOnly: true,
    run: async (page) => {
      const handle = page.locator('[data-ui="panel-handle"]');
      await expect(handle).toBeVisible();
      const before = await handle.getAttribute('aria-valuenow');
      await handle.focus();
      await page.keyboard.press('PageDown');
      // A separator that reports a stale position is worse than one that
      // reports none, so the moved value is the state worth scanning.
      await expect(handle).not.toHaveAttribute('aria-valuenow', before ?? '');
      await expect(handle).toBeFocused();
      await scan(page, 'panel-group handle focused after resizing');
    },
  },
];

for (const theme of THEMES) {
  test.describe(`a11y interaction states (${theme})`, () => {
    for (const c of interactionCases) {
      test(c.id, async ({ browser }) => {
        // The resize handle is `hidden md:block`, so on a phone viewport there
        // is no state to reach rather than a state that is broken.
        test.skip(
          c.desktopOnly === true && test.info().project.name === 'mobile',
          'the control does not exist below the md breakpoint',
        );
        test.setTimeout(30_000 * (c.scans ?? 1) + (c.awaitsVendor === true ? 30_000 : 0));
        const { page, close } = await open(browser, '', theme);
        try {
          await galleryReady(page);
          await c.run(page);
        } finally {
          await close();
        }
      });
    }
  });
}

// --- scenario states -------------------------------------------------------

// The declared states live only in the generated Go descriptor table; nothing
// exports them to TypeScript. Reading that file is what keeps this matrix
// honest — a scenario that gains or loses a state changes this suite with no
// edit here, whereas a literal list would quietly stop covering it and still
// go green.
const SCENARIOS_GEN = join(__dirname, '..', 'internal', 'web', 'templates', 'scenarios_gen.go');
const scenarioSource = readFileSync(SCENARIOS_GEN, 'utf8');

function declaredStates(source: string): Map<string, string[]> {
  const found = new Map<string, string[]>();
  // Each record is `{Slug: "x", ... States: []string{"a", "b"}}`. Surfaces is
  // a different key, so the States capture cannot latch onto it.
  for (const match of source.matchAll(/\{Slug: "([a-z-]+)",[\s\S]*?States:\s*\[\]string\{([^}]*)\}/g)) {
    found.set(match[1], Array.from(match[2].matchAll(/"([a-z-]+)"/g), (s) => s[1]));
  }
  return found;
}

const scenarioStates = declaredStates(scenarioSource);
const scenarioPersona = new Map(
  surfaces.filter((s) => s.kind === 'scenario').map((s) => [s.path.split('/').pop() ?? '', s.persona]),
);

test.describe('scenario state matrix', () => {
  test('covers every declared scenario', () => {
    // The parse above is a regex over generated Go, so it has to be proven
    // total: a record it skipped would silently drop that scenario's states
    // from the matrix while the suite still reported green.
    const records = (scenarioSource.match(/\{Slug: "/g) ?? []).length;
    expect(records, 'no scenario records parsed').toBeGreaterThan(0);
    expect(scenarioStates.size).toBe(records);
    for (const [slug, states] of scenarioStates) {
      expect(states.length, `${slug} declares no state`).toBeGreaterThan(0);
      expect(scenarioPersona.has(slug), `${slug} has no generated visual surface`).toBe(true);
    }
  });
});

for (const theme of THEMES) {
  test.describe(`a11y scenario states (${theme})`, () => {
    for (const [slug, states] of scenarioStates) {
      const persona = scenarioPersona.get(slug) ?? '';
      for (const state of states) {
        // The visual matrix and a11y.spec.ts only ever see each scenario's
        // default state; the loading, empty, error, success and read-only
        // compositions are different markup that has never been scanned.
        test(`${slug}?state=${state}`, async ({ browser }) => {
          const { page, close } = await open(browser, persona, theme);
          try {
            await page.goto(`/dev/scenarios/${slug}?state=${state}`);
            await expect(page.locator(persona ? 'main' : 'body')).toBeVisible();
            await scan(page, `${slug}?state=${state}`);
          } finally {
            await close();
          }
        });
      }
    }
  });
}
