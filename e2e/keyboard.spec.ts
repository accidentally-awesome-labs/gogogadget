import { test, expect, type Locator, type Page } from '@playwright/test';

// Every component that declares a keyboard model in the generated reference
// (`internal/web/templates/ui/reference_gen.go`) renders a live instance on the
// dev catalog index, so the whole file drives one surface. It needs no persona:
// the dev routes are gated on DEV_AUTH_BYPASS, not on a session.
const GALLERY = '/dev/gallery';

// htmx owns every request in this app, including the one a kanban drop issues.
// Declaring the one method the drag path uses keeps that call type-checked
// instead of asserted: if htmx renames `trigger`, this file stops compiling
// rather than silently testing nothing.
declare global {
  interface Window {
    htmx: { trigger: (element: Element, name: string) => void };
  }
}

// Alpine registers every controller on `alpine:init`, and uiTree.init is what
// applies role="tree" to markup that ships without it. Waiting on that
// attribute waits on the controllers having run — without it these tests race
// the deferred bundle and assert the native fallback's behaviour instead of the
// enhanced one they exist to check.
async function boot(page: Page): Promise<void> {
  await page.goto(GALLERY);
  await expect(page.locator('#gallery-tree')).toHaveAttribute('role', 'tree');
}

// activeText is what a reader would hear from wherever focus landed. Asserting
// on it rather than on a selector is what makes a failed roving-focus
// assertion say which row or cell focus actually reached.
function activeText(page: Page): Promise<string> {
  return page.evaluate(() =>
    (document.activeElement?.textContent ?? '').replace(/\s+/g, ' ').trim(),
  );
}

// focusInside reports whether focus is still within this element. A menu that
// opens and leaves focus outside itself has no arrow navigation to offer.
function focusInside(scope: Locator): Promise<boolean> {
  return scope.evaluate((el) => el.contains(document.activeElement));
}

// focusEscapedModal names the control focus reached outside a modal, or null.
//
// Chromium wraps a modal dialog's tab cycle through the document body, so body
// is the one element outside the dialog that legitimately appears mid-cycle.
// What must never appear is a control behind the backdrop — that is focus
// genuinely escaping, and it drops the user onto a page they cannot see.
function focusEscapedModal(dialog: Locator): Promise<string | null> {
  return dialog.evaluate((el) => {
    const active = document.activeElement;
    if (!active || active === document.body || el.contains(active)) return null;
    return active.outerHTML.slice(0, 120);
  });
}

// tabStops counts the descendants Tab can actually reach: tabbable *and*
// rendered. Visibility is half the question — an `x-show`-hidden menu panel
// keeps tabIndex 0 on every command while being display:none, so counting the
// property alone would report a panel that is not in the tab order at all.
//
// A roving tabindex exists to hold this count at one: a stop per cell or per
// node turns Tab into one press per item just to get past the widget.
function tabStops(scope: Locator, selector: string): Promise<number> {
  return scope.evaluate(
    (el, sel) =>
      Array.from(el.querySelectorAll(sel)).filter((node) => {
        const candidate = node as HTMLElement;
        return candidate.tabIndex === 0 && candidate.checkVisibility();
      }).length,
    selector,
  );
}

// menuOf resolves one uiMenu widget by its trigger's accessible name. Every
// menu on the catalog is named for what it acts on, so the name is the stable
// handle — the widget root carries no test id. `within` narrows to a container
// when a name is not unique page-wide (the catalog renders several controls
// called "Edit").
function menuOf(page: Page, name: string, within?: string) {
  const scope = within ? page.locator(within) : page;
  const root = scope.locator('[data-ui=dropdown-menu]', {
    has: page.getByRole('button', { name, exact: true }),
  });
  const panel = root.locator('[data-ui-menu-panel]');
  return {
    trigger: root.getByRole('button', { name, exact: true }),
    root,
    panel,
    items: panel.locator('a[href], button'),
  };
}

// openMenuByKeyboard is the path a keyboard user has: focus the trigger, press
// Enter. Clicking would prove nothing about the keyboard contract.
async function openMenuByKeyboard(page: Page, name: string, within?: string) {
  const menu = menuOf(page, name, within);
  await menu.trigger.focus();
  await page.keyboard.press('Enter');
  await expect(menu.panel).toBeVisible();
  return menu;
}

// Both "Delete project" controls on the catalog are buttons, so the
// confirm-action trigger is addressed through the root that owns its dialog.
const CONFIRM_ACTION = '[data-ui=confirm-action]:has(#gallery-confirm)';

// dropdown-menu and context-menu declare the same key model and share the
// uiMenu controller, and the catalog gives both the same three commands —
// Rename, Duplicate, Delete — so one expectation covers both surfaces.
// Archive sits between them as an aria-disabled span: a disabled command is
// not a command, and an arrow key that lands on one is a press to repeat.
const MENU_CASES = [
  { component: 'dropdown-menu', trigger: 'Actions' },
  { component: 'context-menu', trigger: 'Row actions' },
] as const;

test.describe('focus return', () => {
  // A native <dialog> returns focus to whatever was focused before
  // showModal(). These pin that, because the trigger is a plain button with an
  // Alpine click handler: an implementation that opened the dialog by any
  // other route (moving the node, re-rendering the trigger) would silently
  // lose the return target and leave a keyboard user at the top of the page.
  const modals: Array<{ name: string; trigger: string; dialog: string; scope?: string }> = [
    { name: 'dialog', trigger: 'Open dialog', dialog: '#gallery-dialog' },
    { name: 'alert-dialog', trigger: 'Confirm delete', dialog: '#gallery-alert' },
    { name: 'drawer', trigger: 'Open drawer', dialog: '#gallery-drawer' },
    {
      name: 'confirm-action',
      trigger: 'Delete project',
      dialog: '#gallery-confirm',
      scope: CONFIRM_ACTION,
    },
  ];

  for (const modal of modals) {
    test(`${modal.name} returns focus to its trigger`, async ({ page }) => {
      await boot(page);
      const scope = modal.scope ? page.locator(modal.scope) : page;
      const trigger = scope.getByRole('button', { name: modal.trigger, exact: true });
      const dialog = page.locator(modal.dialog);

      await trigger.focus();
      await page.keyboard.press('Enter');
      await expect(dialog).toBeVisible();
      expect(await focusInside(dialog)).toBe(true);

      await page.keyboard.press('Escape');
      await expect(dialog).toBeHidden();
      await expect(trigger).toBeFocused();
    });
  }

  test('dropdown-menu moves focus into the panel and back to the trigger', async ({ page }) => {
    await boot(page);
    const menu = await openMenuByKeyboard(page, 'Actions');

    // The first command, not the panel: landing on a non-focusable container
    // would leave the arrow keys with nothing to move from.
    await expect(menu.items.first()).toBeFocused();

    await page.keyboard.press('Escape');
    await expect(menu.panel).toBeHidden();
    await expect(menu.trigger).toBeFocused();
  });

  test('context-menu returns focus to its trigger from both open paths', async ({ page }) => {
    await boot(page);
    const region = page.locator('[data-ui=context-menu] [data-ui-context-region]');
    const menu = menuOf(page, 'Row actions');

    // Right-click is the shortcut. It is unreachable by keyboard, so the
    // component's own trigger has to be the mechanism — and dismissing after a
    // right-click still has to leave focus somewhere a keyboard user can use.
    await region.click({ button: 'right' });
    await expect(menu.panel).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(menu.panel).toBeHidden();
    await expect(menu.trigger).toBeFocused();

    await openMenuByKeyboard(page, 'Row actions');
    await page.keyboard.press('Escape');
    await expect(menu.trigger).toBeFocused();
  });

  test('popover closes on Escape and leaves focus on its trigger', async ({ page }) => {
    await boot(page);
    const root = page.locator('[data-ui=popover]');
    const trigger = root.getByRole('button', { name: 'Details', exact: true });
    const panel = root.locator('[data-ui-menu-panel]');

    await trigger.focus();
    await page.keyboard.press('Enter');
    await expect(panel).toBeVisible();
    await expect(trigger).toHaveAttribute('aria-expanded', 'true');

    // The contract says focus moves into the panel. This popover's panel is
    // prose with nothing focusable in it, so the shared uiMenu controller
    // finds no command to move to and focus stays on the trigger. Pinned
    // because the alternative — focusing the panel container — would be a tab
    // stop that announces nothing; the move-into-panel half of the claim is
    // honoured wherever the panel holds commands, which dropdown-menu covers.
    await expect(trigger).toBeFocused();
    expect(await focusInside(panel)).toBe(false);

    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await expect(trigger).toBeFocused();
  });

  test('command palette returns focus to the trigger that opened it', async ({ page }) => {
    await boot(page);
    const trigger = page.getByRole('button', { name: /Search commands/ });
    const dialog = page.locator('#gallery-command');
    const input = dialog.locator('[data-command-input]');

    await trigger.focus();
    await page.keyboard.press('Enter');
    await expect(dialog).toBeVisible();
    // The input, so the first keystroke after opening is a search rather than
    // being swallowed by the dialog box itself.
    await expect(input).toBeFocused();

    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  });

  test('hover-card opens on focus and Escape closes it without moving focus', async ({ page }) => {
    await boot(page);
    // Scoped to the first of the catalog's two hover cards by the panel it
    // controls. The second one is what makes this a real test of instance
    // isolation rather than of "a hover card opened": a controller that shared
    // state between instances would open both from one focus, and an unscoped
    // trigger locator could not tell the difference.
    const trigger = page.locator('[data-ui-hovercard-trigger][aria-describedby="gallery-hovercard"]');
    const panel = page.locator('#gallery-hovercard');
    const otherPanel = page.locator('#gallery-hovercard-second');

    await expect(panel).toBeHidden();
    await trigger.focus();
    await expect(panel).toBeVisible();
    await expect(otherPanel).toBeHidden();

    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    // Focus must not move: the user is mid-Tab through the row, and a card
    // that steals focus on dismissal would restart that journey.
    await expect(trigger).toBeFocused();
  });
});

test.describe('focus trap', () => {
  // showModal() is what supplies the trap. These assert it for real rather
  // than trusting the platform, because a stray element rendered outside the
  // <dialog> — a portal, a toast — would break it invisibly.
  for (const [name, trigger, selector] of [
    ['dialog', 'Open dialog', '#gallery-dialog'],
    ['drawer', 'Open drawer', '#gallery-drawer'],
  ] as const) {
    test(`Tab and Shift+Tab stay inside the modal ${name}`, async ({ page }) => {
      await boot(page);
      const opener = page.getByRole('button', { name: trigger, exact: true });
      const dialog = page.locator(selector);

      await opener.focus();
      await page.keyboard.press('Enter');
      await expect(dialog).toBeVisible();

      // More presses than the dialog has stops, so the cycle is exercised
      // past its own end: a trap that only holds for one lap is not a trap.
      const stops = await dialog.locator('button, input, select, textarea, a[href]').count();
      expect(stops).toBeGreaterThan(0);
      for (let i = 0; i < stops + 2; i++) {
        await page.keyboard.press('Tab');
        expect(await focusEscapedModal(dialog)).toBeNull();
      }
      for (let i = 0; i < stops + 2; i++) {
        await page.keyboard.press('Shift+Tab');
        expect(await focusEscapedModal(dialog)).toBeNull();
      }

      await page.keyboard.press('Escape');
      await expect(dialog).toBeHidden();
      await expect(opener).toBeFocused();
    });
  }

  test('alert-dialog opens with Cancel focused so a stray Enter cannot confirm', async ({
    page,
  }) => {
    await boot(page);
    const dialog = page.locator('#gallery-alert');

    await page.getByRole('button', { name: 'Confirm delete', exact: true }).focus();
    // Enter is the key that opened the dialog, so it is the key most likely to
    // be pressed again a moment later. It must not land on Delete.
    await page.keyboard.press('Enter');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Keep it' })).toBeFocused();
  });

  test('confirm-action opens with Cancel focused', async ({ page }) => {
    await boot(page);
    const dialog = page.locator('#gallery-confirm');

    await page
      .locator(CONFIRM_ACTION)
      .getByRole('button', { name: 'Delete project', exact: true })
      .focus();
    await page.keyboard.press('Enter');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Keep it' })).toBeFocused();
  });
});

test.describe('roving tabindex', () => {
  test('tree exposes one tab stop and it follows the focused node', async ({ page }) => {
    await boot(page);
    const tree = page.locator('#gallery-tree');
    const items = tree.locator('[data-tree-item]');

    expect(await items.count()).toBeGreaterThan(2);
    expect(await tabStops(tree, '[data-tree-item]')).toBe(1);
    await expect(items.first()).toHaveAttribute('tabindex', '0');

    await items.first().focus();
    await page.keyboard.press('ArrowDown');
    expect(await tabStops(tree, '[data-tree-item]')).toBe(1);
    // The stop has to be the focused node, not the first one: after Tab out
    // and Tab back the user must land where they left off.
    await expect(items.nth(1)).toHaveAttribute('tabindex', '0');
    await expect(items.first()).toHaveAttribute('tabindex', '-1');
  });

  test('data-grid exposes one cell tab stop and it follows the focused cell', async ({ page }) => {
    await boot(page);
    const table = page.locator('#gallery-grid [data-grid-table]');

    expect(await tabStops(table, 'th, td')).toBe(1);
    const first = table.locator('thead th').first();
    await expect(first).toHaveAttribute('tabindex', '0');

    await first.focus();
    await page.keyboard.press('ArrowRight');
    expect(await tabStops(table, 'th, td')).toBe(1);
    await expect(table.locator('thead th').nth(1)).toHaveAttribute('tabindex', '0');
    await expect(first).toHaveAttribute('tabindex', '-1');
  });

  test('tab-panels gives the set one tab stop', async ({ page }) => {
    await boot(page);
    const tablist = page.getByRole('tablist', { name: 'Environment' });
    const tabs = tablist.getByRole('tab');

    expect(await tabs.count()).toBe(3);
    expect(await tabStops(tablist, '[role=tab]')).toBe(1);

    await tabs.first().focus();
    await page.keyboard.press('ArrowRight');
    expect(await tabStops(tablist, '[role=tab]')).toBe(1);
    await expect(tabs.nth(1)).toHaveAttribute('tabindex', '0');
    await expect(tabs.nth(1)).toHaveAttribute('aria-selected', 'true');
  });

  test('a closed dropdown-menu is a single tab stop', async ({ page }) => {
    await boot(page);
    // The declared contract makes this a disclosure, not an ARIA menu: there
    // is no roving tabindex over the commands. What must hold instead is that
    // a closed panel is out of the tab order entirely — an inert-but-tabbable
    // panel is a run of invisible stops a keyboard user cannot escape.
    const menu = menuOf(page, 'Actions');
    await expect(menu.panel).toBeHidden();
    expect(await tabStops(menu.root, 'a[href], button')).toBe(1);
    await expect(menu.trigger).toHaveAttribute('aria-expanded', 'false');

    await openMenuByKeyboard(page, 'Actions');
    // Open, the commands are reachable — which is the point of opening it.
    expect(await tabStops(menu.root, 'a[href], button')).toBeGreaterThan(1);
  });

  test('menubar is one tab stop per menu with every panel closed', async ({ page }) => {
    await boot(page);
    const bar = page.locator('[data-ui=menubar]');
    const menus = bar.locator('[data-ui=dropdown-menu]');

    const count = await menus.count();
    expect(count).toBe(2);
    // One stop per menu and not one per command: the bar is a row of
    // disclosures, and its contract explicitly declines cross-menu arrows.
    expect(await tabStops(bar, 'a[href], button')).toBe(count);
  });
});

test.describe('typeahead', () => {
  // Each menu's contract promises a single printable character jumping to the
  // next matching command. Pressing the same key twice therefore has to
  // advance to the second match, not search for a two-letter string — that
  // difference is the whole distinction between this model and the tree's.
  for (const menu of MENU_CASES) {
    test(`${menu.component} jumps to the next command on a printable key`, async ({ page }) => {
      await boot(page);
      const open = await openMenuByKeyboard(page, menu.trigger);
      await expect(open.items.first()).toBeFocused();

      await page.keyboard.press('d');
      expect(await activeText(page)).toBe('Duplicate');
      await page.keyboard.press('d');
      expect(await activeText(page)).toBe('Delete');
    });
  }

  test('menubar menus carry the same typeahead', async ({ page }) => {
    await boot(page);
    const open = await openMenuByKeyboard(page, 'Edit', '[data-ui=menubar]');
    await expect(open.items.first()).toBeFocused();
    expect(await activeText(page)).toBe('Undo');

    await page.keyboard.press('d');
    expect(await activeText(page)).toBe('Delete');
  });

  test('tree typeahead buffers multiple characters and resets after a pause', async ({ page }) => {
    await boot(page);
    const tree = page.locator('#gallery-tree');
    await tree.locator('[data-tree-item]').first().focus();
    expect(await activeText(page)).toBe('internal');

    await page.keyboard.press('r');
    expect(await activeText(page)).toBe('routes.go');

    // Within the buffer window the second key extends the search rather than
    // starting a new one, so "re" finds registry and not the next r-node.
    await page.keyboard.press('e');
    expect(await activeText(page)).toBe('registry');

    // The reset is a wall-clock rule — uiTree drops the buffer after 800ms of
    // silence — so elapsed time is the condition and there is nothing else to
    // wait for. After the pause the buffer is a fresh "r": without the reset
    // the search would be "rer", match nothing, and focus would stay on
    // registry, so reaching README.md is what proves the buffer cleared.
    await page.waitForTimeout(900);
    await page.keyboard.press('r');
    expect(await activeText(page)).toBe('README.md');
  });
});

test.describe('arrow, home, end and page keys', () => {
  test('tree arrows move in, out, up and down', async ({ page }) => {
    await boot(page);
    const tree = page.locator('#gallery-tree');
    await tree.locator('[data-tree-item]').first().focus();
    expect(await activeText(page)).toBe('internal');

    await page.keyboard.press('ArrowDown');
    expect(await activeText(page)).toBe('web');
    await page.keyboard.press('ArrowUp');
    expect(await activeText(page)).toBe('internal');

    // On an open branch ArrowRight steps into it; the branch is already open,
    // so it must move rather than re-opening what is open.
    await page.keyboard.press('ArrowRight');
    expect(await activeText(page)).toBe('web');
    // From a leaf ArrowLeft always means "out", to the owning branch.
    await page.keyboard.press('ArrowDown');
    expect(await activeText(page)).toBe('templates');
    await page.keyboard.press('ArrowLeft');
    expect(await activeText(page)).toBe('web');

    await page.keyboard.press('End');
    expect(await activeText(page)).toBe('README.md');
    await page.keyboard.press('Home');
    expect(await activeText(page)).toBe('internal');
  });

  test('tree ArrowRight opens a closed branch and ArrowLeft closes an open one', async ({
    page,
  }) => {
    await boot(page);
    const tree = page.locator('#gallery-tree');
    const registry = tree.locator('[data-tree-node="n-registry"] > details');

    await tree.locator('[data-tree-item]').first().focus();
    await page.keyboard.press('End');
    // README.md is last; the closed branch above it is registry.
    await page.keyboard.press('ArrowUp');
    expect(await activeText(page)).toBe('registry');

    await expect(registry).not.toHaveAttribute('open', '');
    await page.keyboard.press('ArrowRight');
    await expect(registry).toHaveAttribute('open', '');
    await page.keyboard.press('ArrowLeft');
    await expect(registry).not.toHaveAttribute('open', '');
  });

  test('data-grid arrows, Home/End and Page keys move cell focus', async ({ page }) => {
    await boot(page);
    const table = page.locator('#gallery-grid [data-grid-table]');

    await table.locator('thead th').first().focus();
    await page.keyboard.press('ArrowDown');
    expect(await activeText(page)).toContain('Apollo');

    await page.keyboard.press('ArrowRight');
    expect(await activeText(page)).toBe('Ada');
    await page.keyboard.press('ArrowLeft');
    expect(await activeText(page)).toContain('Apollo');

    // Home and End are row-local, which is what makes them useful in a wide
    // table: the user wants this row's ends, not the grid's corners.
    await page.keyboard.press('End');
    expect(await activeText(page)).toBe('eu-west-1');
    await page.keyboard.press('Home');
    expect(await activeText(page)).toContain('Apollo');

    // With Ctrl/Cmd they become the grid's extremes.
    await page.keyboard.press('Control+End');
    expect(await activeText(page)).toBe('eu-central-1');
    await page.keyboard.press('Control+Home');
    expect(await activeText(page)).toContain('Project');

    // Ten rows, clamped to the grid: this table has five, so PageDown lands on
    // the last row rather than moving nowhere.
    await page.keyboard.press('PageDown');
    expect(await activeText(page)).toContain('Deimos');
    await page.keyboard.press('PageUp');
    expect(await activeText(page)).toContain('Project');
  });

  test('data-grid binds its keys to the table, not the document', async ({ page }) => {
    await boot(page);
    const table = page.locator('#gallery-grid [data-grid-table]');
    const search = page.locator('[data-grid-for="gallery-grid"] input[type=search]');

    await table.locator('thead th').first().focus();
    await page.keyboard.press('ArrowDown');
    const cellBefore = await activeText(page);

    await search.fill('apollo');
    await page.keyboard.press('ArrowLeft');
    // A document-level handler would move the grid's cell focus from under
    // this field and make every search box inside a grid impossible to edit.
    await expect(search).toBeFocused();
    expect(await search.evaluate((el: HTMLInputElement) => el.selectionStart)).toBe(5);

    await table.locator('td').first().focus();
    expect(await activeText(page)).toBe(cellBefore);
  });

  test('tab-panels arrows wrap and Home/End jump to the ends', async ({ page }) => {
    await boot(page);
    const tablist = page.getByRole('tablist', { name: 'Environment' });
    const tabs = tablist.getByRole('tab');
    const first = tabs.nth(0);
    const last = tabs.nth(2);

    await first.focus();
    await page.keyboard.press('ArrowRight');
    await expect(tabs.nth(1)).toBeFocused();
    await page.keyboard.press('ArrowLeft');
    await expect(first).toBeFocused();

    // Wrapping in both directions, so the set has no dead end.
    await page.keyboard.press('ArrowLeft');
    await expect(last).toBeFocused();
    await page.keyboard.press('ArrowRight');
    await expect(first).toBeFocused();

    await page.keyboard.press('End');
    await expect(last).toBeFocused();
    await expect(last).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Home');
    await expect(first).toBeFocused();
    // Automatic activation: the panel follows focus, so arrowing through the
    // set shows each panel rather than requiring a second key.
    await expect(page.locator('#tab-dev')).toBeVisible();
    await expect(page.locator('#tab-prod')).toBeHidden();
  });

  test('menubar has no cross-menu arrow navigation', async ({ page }) => {
    await boot(page);
    const file = await openMenuByKeyboard(page, 'File', '[data-ui=menubar]');
    await expect(file.items.first()).toBeFocused();

    // Declared absent on purpose: half an ARIA menubar would promise a
    // contract the widget does not honour. ArrowRight must therefore do
    // nothing rather than jump to the Edit menu.
    await page.keyboard.press('ArrowRight');
    expect(await focusInside(file.root)).toBe(true);
    expect(await activeText(page)).toBe('New');

    await page.keyboard.press('ArrowDown');
    expect(await activeText(page)).toBe('Open');
    expect(await focusInside(file.root)).toBe(true);

    // Escape is the way out of an opened menu, and it has to hand focus back:
    // a bar of five menus where dismissing one drops focus to the page top
    // makes reaching the next menu a full Tab journey.
    await page.keyboard.press('Escape');
    await expect(file.panel).toBeHidden();
    await expect(file.trigger).toBeFocused();
  });

  for (const menu of MENU_CASES) {
    test(`${menu.component} arrows and Home/End move between commands`, async ({ page }) => {
      await boot(page);
      const open = await openMenuByKeyboard(page, menu.trigger);
      expect(await activeText(page)).toBe('Rename');

      await page.keyboard.press('ArrowDown');
      expect(await activeText(page)).toBe('Duplicate');
      await page.keyboard.press('ArrowUp');
      expect(await activeText(page)).toBe('Rename');

      // Wrapping, so a long menu's last command is one press from its first
      // rather than the length of the menu away.
      await page.keyboard.press('ArrowUp');
      expect(await activeText(page)).toBe('Delete');

      await page.keyboard.press('Home');
      expect(await activeText(page)).toBe('Rename');
      await page.keyboard.press('End');
      expect(await activeText(page)).toBe('Delete');
      expect(await focusInside(open.panel)).toBe(true);
    });
  }

  test('panel-handle moves the split by one, ten, and to its bounds', async ({ page }) => {
    await boot(page);
    const handle = page.locator('#gallery-panels-handle-1');
    const now = () => handle.getAttribute('aria-valuenow');

    await expect(handle).toHaveRole('separator');
    await expect(handle).toHaveAttribute('aria-valuemin', '20');
    await expect(handle).toHaveAttribute('aria-valuemax', '75');
    expect(await now()).toBe('35');

    await handle.focus();
    await page.keyboard.press('ArrowRight');
    expect(await now()).toBe('36');
    await page.keyboard.press('ArrowLeft');
    expect(await now()).toBe('35');

    await page.keyboard.press('PageDown');
    expect(await now()).toBe('45');
    await page.keyboard.press('PageUp');
    expect(await now()).toBe('35');

    await page.keyboard.press('End');
    expect(await now()).toBe('75');
    // Clamped at the bound. Past it the neighbour would be squeezed below its
    // own floor, which hides content with no way to discover it is missing.
    await page.keyboard.press('ArrowRight');
    expect(await now()).toBe('75');

    await page.keyboard.press('Home');
    expect(await now()).toBe('20');
    await page.keyboard.press('ArrowLeft');
    expect(await now()).toBe('20');
  });

  test('carousel arrow keys scroll the focused track', async ({ page }) => {
    // Narrow enough that three 18rem slides overflow. At the desktop measure
    // they all fit, so there is no scrolling to observe and the test would
    // pass or fail on the viewport rather than on the contract.
    await page.setViewportSize({ width: 480, height: 800 });
    await boot(page);
    const track = page.locator('#gallery-carousel [data-carousel-track]');
    const left = () => track.evaluate((el) => el.scrollLeft);

    await expect(track).toHaveAttribute('tabindex', '0');
    expect(await track.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
    expect(await left()).toBe(0);

    await track.focus();
    await page.keyboard.press('ArrowRight');
    // The browser's own scrolling, not a slideshow key model: the contract is
    // explicitly that no widget behaviour is layered over it.
    await expect.poll(left).toBeGreaterThan(0);
  });

  test('scroll-area scrolls with arrow and Page keys', async ({ page }) => {
    await boot(page);
    const region = page.getByRole('region', { name: 'Release notes' });
    const top = () => region.evaluate((el) => el.scrollTop);

    await expect(region).toHaveAttribute('tabindex', '0');
    await region.focus();
    await page.keyboard.press('ArrowDown');
    await expect.poll(top).toBeGreaterThan(0);

    const afterArrow = await top();
    await page.keyboard.press('PageDown');
    await expect.poll(top).toBeGreaterThan(afterArrow);
  });

  test('command palette moves the active option and Enter follows it', async ({ page }) => {
    // The command's destination needs a session, and following it for real
    // would leave this test asserting the sign-in redirect instead of the key.
    // Serving the target locally keeps the assertion on the palette.
    await page.route('**/app/projects', (route) =>
      route.fulfill({ status: 200, contentType: 'text/html', body: '<title>projects</title>' }),
    );
    await boot(page);
    const dialog = page.locator('#gallery-command');
    const input = dialog.locator('[data-command-input]');

    // The shortcut is a document-level binding, so it must work from anywhere
    // on the page rather than only from the trigger.
    await page.getByRole('button', { name: /Search commands/ }).focus();
    await page.keyboard.press('Control+k');
    await expect(dialog).toBeVisible();
    await expect(input).toBeFocused();

    // Focus stays in the input for a combobox; the active option is named by
    // aria-activedescendant rather than being a separate tab stop.
    await expect(input).toHaveAttribute('role', 'combobox');
    await expect(input).not.toHaveAttribute('aria-activedescendant', /./);

    await page.keyboard.press('ArrowDown');
    await expect(input).toHaveAttribute('aria-activedescendant', 'cmd-projects');
    await page.keyboard.press('ArrowDown');
    await expect(input).toHaveAttribute('aria-activedescendant', 'cmd-settings');
    await page.keyboard.press('End');
    await expect(input).toHaveAttribute('aria-activedescendant', 'cmd-new');
    await page.keyboard.press('Home');
    await expect(input).toHaveAttribute('aria-activedescendant', 'cmd-projects');
    await expect(input).toBeFocused();

    await page.keyboard.press('Enter');
    // The highlighted command is a real link, so Enter navigates to it.
    await page.waitForURL('**/app/projects');
  });

  test('command palette Enter submits the search when nothing is highlighted', async ({ page }) => {
    await boot(page);
    const dialog = page.locator('#gallery-command');
    const input = dialog.locator('[data-command-input]');

    await page.getByRole('button', { name: /Search commands/ }).focus();
    await page.keyboard.press('Control+k');
    await expect(input).toBeFocused();

    await input.fill('projects');
    // No ArrowDown, so nothing is active: Enter has to submit the form, which
    // is exactly what it does with no script at all.
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/dev\/gallery\?.*q=projects/);
  });
});

test.describe('date navigation', () => {
  // Each picker declares the same contract: the native input keeps the
  // platform's keyboard entry, and Escape closes the calendar and returns
  // focus to the trigger. The calendar is a lazily loaded engine, so the
  // trigger stays hidden until it can actually open something.
  const pickers: Array<{ component: string; root: string; value: string }> = [
    {
      component: 'date-picker',
      root: '[data-ui=date-picker]:has(input[name="starts_on"])',
      value: '2026-01-15',
    },
    { component: 'date-time-picker', root: '[data-ui=date-time-picker]', value: '2026-01-15T09:30' },
    { component: 'date-range-picker', root: '[data-ui=date-range-picker]', value: '2026-02-01' },
  ];

  for (const picker of pickers) {
    test(`${picker.component} opens by keyboard and Escape returns focus`, async ({ page }) => {
      await boot(page);
      const root = page.locator(picker.root);
      const trigger = root.locator('[data-calendar-trigger]');
      const popover = root.locator('[data-calendar-popover]');
      const input = root.locator('input[type=date], input[type=datetime-local]').first();

      await expect(input).toHaveValue(picker.value);
      // Revealed only once the engine landed: a button that opens nothing is
      // worse than no button.
      await expect(trigger).toBeVisible();
      await expect(popover).toBeHidden();

      await trigger.focus();
      await page.keyboard.press('Enter');
      await expect(popover).toBeVisible();
      await expect(trigger).toHaveAttribute('aria-expanded', 'true');
      // The calendar is reachable: a popover with nothing focusable in it is a
      // region a keyboard user can open and not use.
      expect(await focusInside(root)).toBe(true);
      await expect(popover.locator('calendar-date, calendar-range')).toHaveCount(1);

      await page.keyboard.press('Escape');
      await expect(popover).toBeHidden();
      await expect(trigger).toHaveAttribute('aria-expanded', 'false');
      await expect(trigger).toBeFocused();
      // The input is the submitted value throughout: opening and dismissing a
      // calendar must never edit the field.
      await expect(input).toHaveValue(picker.value);
    });
  }

  test('date-picker keeps the native input authoritative under typed entry', async ({ page }) => {
    await boot(page);
    const root = page.locator('[data-ui=date-picker]:has(input[name="starts_on"])');
    const input = root.locator('input[name="starts_on"]');

    // The platform's own keyboard entry, unmodified: this is what works with
    // the calendar engine absent, so the enhancement must not intercept it.
    await input.focus();
    await input.fill('2026-03-04');
    await expect(input).toHaveValue('2026-03-04');
    await expect(root.locator('[data-calendar-popover]')).toBeHidden();
  });
});

test.describe('markdown editor shortcuts', () => {
  const shortcuts: Array<{ key: string; expected: string }> = [
    { key: 'Control+b', expected: '**catalog**' },
    { key: 'Control+i', expected: '_catalog_' },
    { key: 'Control+k', expected: '[catalog](url)' },
  ];

  for (const shortcut of shortcuts) {
    test(`${shortcut.key} wraps the selection`, async ({ page }) => {
      await boot(page);
      const input = page.locator('[data-ui=markdown-editor]').first().locator('[data-editor-input]');

      await input.focus();
      await input.evaluate((el: HTMLTextAreaElement) => {
        const at = el.value.indexOf('catalog');
        el.setSelectionRange(at, at + 'catalog'.length);
      });
      await page.keyboard.press(shortcut.key);
      // The textarea is the form value, so the syntax has to land in it —
      // there is no separate model that could hold the real content.
      expect(await input.inputValue()).toContain(shortcut.expected);
    });
  }

  test('every other key stays the textarea own', async ({ page }) => {
    await boot(page);
    const input = page.locator('[data-ui=markdown-editor]').first().locator('[data-editor-input]');

    await input.focus();
    await input.evaluate((el: HTMLTextAreaElement) => el.setSelectionRange(0, 0));
    await page.keyboard.type('x');
    // A controller that captured plain keys would make the editor unusable for
    // the one thing it exists to do.
    await expect(input).toHaveValue(/^x## What changed/);
  });
});

test.describe('kanban move-menu parity', () => {
  const BOARD = '#gallery-board';
  const MOVE = '/dev/ui/kanban/move';

  test('every card menu is reachable and operable by keyboard alone', async ({ page }) => {
    await boot(page);
    const cards = page.locator(`${BOARD} [data-kanban-card]`);

    const count = await cards.count();
    expect(count).toBeGreaterThan(0);
    for (let i = 0; i < count; i++) {
      const title = (await cards.nth(i).locator('p').first().innerText()).trim();
      const menu = await openMenuByKeyboard(page, `Actions for ${title}`, BOARD);
      // A destination the card is not already in: moving a card to where it
      // sits is a command that does nothing, so it is never offered.
      await expect(menu.panel.getByRole('button', { name: /^Move to / }).first()).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(menu.trigger).toBeFocused();
    }
  });

  test('the keyboard move posts what the drag posts', async ({ page }) => {
    await boot(page);
    const bodies: string[] = [];
    // Aborted after recording so the board is not re-rendered between the two
    // paths: both must be measured from the same starting state.
    await page.route(`**${MOVE}`, async (route) => {
      expect(route.request().method()).toBe('POST');
      bodies.push(route.request().postData() ?? '');
      await route.abort();
    });

    const menu = await openMenuByKeyboard(page, 'Actions for Split the catalog', BOARD);
    await menu.panel.getByRole('button', { name: 'Move to In progress' }).focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => bodies.length).toBe(1);

    // The drag path fills the card's own move form and submits it, which is
    // exactly what uiKanban.submit does on drop. Driving that form drives the
    // drag's implementation without depending on a synthetic HTML5 drag.
    await page
      .locator(`${BOARD} [data-kanban-card="card-catalog"] [data-kanban-form]`)
      .evaluate((form) => {
        const to = form.querySelector('[data-kanban-to]');
        const position = form.querySelector('[data-kanban-position]');
        if (!(to instanceof HTMLInputElement) || !(position instanceof HTMLInputElement)) {
          throw new Error('the move form is missing the fields a drop fills in');
        }
        to.value = 'doing';
        position.value = '0';
        window.htmx.trigger(form, 'submit');
      });
    await expect.poll(() => bodies.length).toBe(2);

    const [fromMenu, fromDrag] = bodies.map((body) => new URLSearchParams(body));
    // One endpoint, one payload shape. The drag additionally carries the drop
    // index, which the menu has no notion of — but everything identifying the
    // move itself must be identical, or the same server could accept one path
    // and reject the other.
    for (const field of ['card', 'from', 'to']) {
      expect(fromMenu.get(field), `menu ${field}`).toBe(fromDrag.get(field));
    }
    expect(fromMenu.get('card')).toBe('card-catalog');
    expect(fromMenu.get('from')).toBe('backlog');
    expect(fromMenu.get('to')).toBe('doing');
  });

  test('the keyboard move is what actually moves the card', async ({ page }) => {
    await boot(page);
    const card = '[data-kanban-card="card-catalog"]';

    await expect(page.locator(`${BOARD} [data-kanban-column=backlog] ${card}`)).toHaveCount(1);

    const menu = await openMenuByKeyboard(page, 'Actions for Split the catalog', BOARD);
    await menu.panel.getByRole('button', { name: 'Move to In progress' }).focus();
    await page.keyboard.press('Enter');

    // The server's response is authoritative, so the card is where the server
    // put it — not where a client-side optimistic update guessed.
    await expect(page.locator(`${BOARD} [data-kanban-column=doing] ${card}`)).toHaveCount(1);
    await expect(page.locator(`${BOARD} [data-kanban-column=backlog] ${card}`)).toHaveCount(0);
  });
});
