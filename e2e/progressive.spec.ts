import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { sessionFor, type PersonaId } from './generated/personas';

// Progressive enhancement is the promise the whole catalog rests on: a component
// may be *better* with its engine loaded, but it must not be *broken* without it.
// Two guarantees are checked here, neither of which any other spec exercises:
//
//   1. With scripting off, every surface still presents a working native path -
//      or honestly hides the control that would do nothing. A visible button
//      that cannot act is worse than no button: it advertises a capability the
//      page does not have.
//   2. With prefers-reduced-motion set, nothing moves on its own.
//
// Each check asserts the *positive* fallback - the form posts, the region
// discloses, the table carries the numbers - because "no error was thrown" is
// satisfied by a page that renders nothing at all.
//
// `data-ui` is the generated component-identity attribute (the registry and
// TestGalleryCoversEveryComponent both key on it), so it is used to say *which
// component* is under test. Accessible semantics are asserted through
// role/name/label, and state containers through `data-testid`.

const CAROUSEL_VIEWPORT = { width: 420, height: 900 };

// noScript is the whole point of this file: javaScriptEnabled: false cannot be
// set after a context exists, so loginAs (which owns its own context) cannot be
// reused here. The session cookie is built from the same generated persona
// source loginAs uses, so the actor cannot drift from the seeded fixtures.
async function noScript(
  browser: Browser,
  options: { persona?: PersonaId; viewport?: { width: number; height: number } } = {},
): Promise<{ page: Page; context: BrowserContext }> {
  const context = await browser.newContext({
    javaScriptEnabled: false,
    ...(options.viewport ? { viewport: options.viewport } : {}),
  });
  if (options.persona) {
    const base = process.env.E2E_BASE_URL ?? 'http://localhost:18080';
    await context.addCookies([{ name: '__session', value: sessionFor(options.persona), url: base }]);
  }
  return { page: await context.newPage(), context };
}

function defect(description: string) {
  test.info().annotations.push({ type: 'known-defect', description });
}

test.describe('no script — forms', () => {
  // A GET form needs nothing but the browser: this is the baseline the POST
  // forms below are measured against.
  test('the settings form submits its fields', async ({ browser }) => {
    const { page, context } = await noScript(browser, { persona: 'admin' });
    try {
      await page.goto('/dev/scenarios/settings');
      const form = page.getByTestId('settings-account-form');
      await expect(form).toHaveAttribute('method', 'get');
      await form.getByLabel('Display name').fill('Grace Hopper');
      await Promise.all([
        page.waitForURL(/display_name=Grace\+Hopper/),
        form.getByRole('button', { name: 'Save changes' }).click(),
      ]);
      // The browser carried every named field, not just the edited one - which
      // is what makes a no-script submit a complete request rather than a
      // partial one the server has to guess at.
      expect(page.url()).toContain('billing_email=');
      expect(page.url()).toContain('workspace_slug=');
      await expect(page.getByTestId('settings-account-form')).toBeVisible();
    } finally {
      await context.close();
    }
  });

  // The questionnaire is the component that most obviously *could* have been a
  // client-side wizard. It is not: the step is a hidden field, the answers are
  // native controls, and the forward control is a real submit.
  test('the questionnaire posts its answers and its step', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const wizard = page.locator('[data-ui="questionnaire"]');
      await expect(wizard).toHaveAttribute('method', 'post');
      await expect(wizard).toHaveAttribute('action', '/dev/gallery');
      const answer = wizard.locator('input[type="checkbox"]').first();
      const answerValue = await answer.getAttribute('value');
      await answer.check();
      const posted = page.waitForRequest(
        (r) => r.method() === 'POST' && r.url().includes('/dev/gallery'),
      );
      await wizard.getByRole('button', { name: 'Next' }).click();
      const body = (await posted).postData() ?? '';
      // The step travels with the answer. Without it the server would have to
      // infer which question was answered, which is exactly the state a
      // reloaded or back-buttoned wizard cannot be trusted to report.
      expect(body).toContain('step=');
      expect(body).toContain('action=next');
      expect(decodeURIComponent(body)).toContain(answerValue ?? '');
    } finally {
      await context.close();
    }
  });

  test('a POST form is accepted without script', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const form = page.getByTestId('dev-calendar-form');
      await form.locator('input[name="dev_date"]').fill('2026-03-04');
      const answered = page.waitForResponse((r) => r.url().includes('/dev/ui/calendar/select'));
      await form.getByRole('button', { name: 'Use this date' }).click();
      expect((await answered).status()).toBe(200);
    } finally {
      await context.close();
    }
  });

  test('the search input can be submitted', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      // An input with no form ancestor has no submit path at all: Enter does
      // nothing and there is no button to press.
      const search = page.getByTestId('gallery-search');
      await expect(search.locator('xpath=ancestor::form')).toHaveCount(1);
    } finally {
      await context.close();
    }
  });
});

test.describe('no script — disclosure', () => {
  test('the accordion discloses and stays exclusive', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const accordion = page.locator('[data-ui="accordion"]').first();
      const openPanel = page.getByText('native name attribute closes the others');
      const closedPanel = page.getByText('Exclusivity survives with JavaScript disabled');
      await expect(openPanel).toBeVisible();
      await expect(closedPanel).toBeHidden();
      await accordion.locator('summary', { hasText: 'Does it need script?' }).click();
      await expect(closedPanel).toBeVisible();
      // The `name` attribute is what makes the set exclusive. Without it an
      // accordion needs script to close its siblings, and closing them is the
      // only thing that makes it an accordion rather than three disclosures.
      await expect(openPanel).toBeHidden();
    } finally {
      await context.close();
    }
  });

  test('the disclosure discloses', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const body = page.getByText('Toggle, button semantics, keyboard activation');
      await expect(body).toBeHidden();
      await page
        .locator('[data-ui="disclosure"] summary', { hasText: 'What does native details give us?' })
        .click();
      await expect(body).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('a tree branch collapses and reopens', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const tree = page.locator('[data-ui="tree"]');
      // `web` is the deepest server-rendered branch; the lazy ones below it
      // only ever hold a "Loading…" placeholder without script.
      const branch = tree.locator('summary', { hasText: 'web' });
      const leaf = tree.getByText('routes.go');
      await expect(leaf).toBeVisible();
      await branch.click();
      await expect(leaf).toBeHidden();
      await branch.click();
      await expect(leaf).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('the column picker is hidden rather than inert', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const picker = page.locator('[data-ui="column-picker"]');
      // Hiding columns is a client-only convenience: the server sends every
      // column and knows nothing about the choice. So the control is hidden
      // until uiGrid reveals it, rather than sitting there doing nothing.
      await expect(picker).toBeHidden();
      await expect(picker).toHaveAttribute('hidden', /.*/);
    } finally {
      await context.close();
    }
  });

  test('the collapsible discloses its region', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const region = page.getByText('A region toggled from a control elsewhere in the layout.');
      await expect(region).toBeHidden();
      // A native summary, like the accordion and disclosure above: the trigger
      // is the platform's disclosure control rather than a button an Alpine
      // component has to wire up, which is the only shape that discloses here.
      await page
        .locator('[data-ui="collapsible"] summary', { hasText: 'Filters' })
        .click();
      await expect(region).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('the dropdown menu discloses its items', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const menu = page.locator('[data-ui="dropdown-menu"]').first();
      const panel = menu.locator('[data-ui-menu-panel]');
      // Closed first: a panel that is merely present would make the assertion
      // below pass without the trigger doing anything.
      await expect(panel).toBeHidden();
      await menu.locator('[data-ui-menu-trigger]').click();
      await expect(panel).toBeVisible();
      // And its commands are reachable, not just painted: a destination in a
      // menu is a real link, so it survives with no script to send it.
      await expect(panel.locator('a[href]').first()).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('the popover discloses its content', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const popover = page.locator('[data-ui="popover"]').first();
      const panel = popover.locator('[data-ui-menu-panel]');
      await expect(panel).toBeHidden();
      await popover.getByRole('button', { name: 'Details' }).click();
      await expect(panel).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('the tab panels fall back to sequential panels', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      // Every panel, in order, not only the selected one - the whole point of
      // the claim is that no content is lost when the tab bar cannot work.
      for (const body of [
        'Arrow keys move between tabs; Home and End jump to the ends.',
        'Exactly one tab is in the tab order.',
        'Each panel is labelled by its tab.',
      ]) {
        await expect(page.getByText(body)).toBeVisible();
      }
      // And the tabs themselves are absent rather than dead: a row of buttons
      // that only uiTabs can operate advertises a control the page lacks.
      await expect(page.locator('[data-ui="tab-panels"] [role="tab"]').first()).toBeHidden();
    } finally {
      await context.close();
    }
  });
});

test.describe('no script — dialogs', () => {
  // A dialog cannot be *opened* without script, so the dismiss path can only be
  // observed on an already-open dialog. Adding `open` to the served markup is
  // exactly what a server rendering an open dialog would emit, and everything
  // after it - the top layer, the form method="dialog" submit, the close - is
  // platform behaviour with no page script involved.
  async function withOpenDialog(page: Page, id: string) {
    await page.route('**/dev/gallery', async (route) => {
      const response = await route.fetch();
      const body = (await response.text()).replace(`<dialog id="${id}"`, `<dialog open id="${id}"`);
      await route.fulfill({ response, body });
    });
    await page.goto('/dev/gallery');
  }

  test('a dialog dismisses through its form method="dialog" close control', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await withOpenDialog(page, 'gallery-dialog');
      const dialog = page.locator('[data-ui="dialog"]');
      await expect(dialog).toBeVisible();
      const close = dialog.getByRole('button', { name: 'Close' });
      await expect(close).toHaveAttribute('type', 'submit');
      await expect(dialog.locator('form')).toHaveAttribute('method', 'dialog');
      await close.click();
      await expect(dialog).toBeHidden();
    } finally {
      await context.close();
    }
  });

  test('an alert dialog cancels through its own submit control', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await withOpenDialog(page, 'gallery-confirm');
      // The dialog that was opened, by the same id, not whichever alert-dialog
      // sorts first: /dev/gallery renders three, and `.first()` retargeted
      // silently on any reorder or new fixture — the visible/role/"Keep it"
      // assertions below would then be about a dialog this test never opened.
      const dialog = page.locator('#gallery-confirm');
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveAttribute('role', 'alertdialog');
      // Cancel is a submit with a value, not a scripted handler. A destructive
      // confirmation is the last place a keyboard user should be trapped
      // because a bundle failed to load.
      const cancel = dialog.getByRole('button', { name: 'Keep it' });
      await expect(cancel).toHaveAttribute('type', 'submit');
      await expect(cancel).toHaveAttribute('value', 'cancel');
      // Activated from the keyboard: a non-modal open dialog sits in flow and a
      // later sibling paints over it, and the keyboard path is the one that
      // matters most for a modal anyway.
      await cancel.press('Enter');
      await expect(dialog).toBeHidden();
    } finally {
      await context.close();
    }
  });
});

test.describe('no script — links', () => {
  test('pagination is a real href that navigates', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const pager = page.getByTestId('gallery-pager').locator('[data-ui="pagination"]');
      // Named by its visible text. It used to be findable as "Page 3" only
      // because pageLink applied aria-label="Page N" to every link including
      // the direction controls, overriding their own text and making Next
      // indistinguishable from a numbered link. That was the defect; the
      // subject of this test — a real href that navigates with no script — is
      // unchanged. This pager is not Numbered, so Previous/Next are its only
      // links and their labels come from PagerLabels' defaults.
      const next = pager.getByRole('link', { name: 'Next' });
      await expect(next).toHaveAttribute('href', '/dev/ui/pagination/page?page=3');
      const answered = page.waitForResponse(
        (r) => r.url().includes('/dev/ui/pagination/page') && r.request().resourceType() === 'document',
      );
      await next.click();
      expect((await answered).status()).toBe(200);
      // The server, not htmx, decided what page 3 is. The same URL is what the
      // enhanced path pushes, so a shared link lands on the same rows.
      await expect(page.getByText('Page 3 of 5.')).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test('a column sort link navigates and the server re-sorts', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const table = page.getByTestId('dev-table');
      await expect(table.locator('th[aria-sort="ascending"]')).toHaveCount(1);
      const sort = table.locator('[data-ui="column-header"] a').first();
      await expect(sort).toHaveAttribute('href', '/dev/ui/table/sort?sort=name&dir=desc');
      const answered = page.waitForResponse(
        (r) => r.url().includes('/dev/ui/table/sort') && r.request().resourceType() === 'document',
      );
      await sort.click();
      expect((await answered).status()).toBe(200);
      // aria-sort moved with the data. Sorting that only reorders rows in the
      // DOM leaves a screen reader reading the old direction.
      await expect(page.locator('th[aria-sort="descending"]')).toHaveCount(1);
    } finally {
      await context.close();
    }
  });

  test('scenario pagination navigates', async ({ browser }) => {
    const { page, context } = await noScript(browser, { persona: 'admin' });
    try {
      await page.goto('/dev/scenarios/resource-list');
      const pager = page.getByTestId('resource-list-pager');
      await Promise.all([
        page.waitForURL(/resource-list\?page=2/),
        pager.getByRole('link', { name: 'Page 2' }).first().click(),
      ]);
      await expect(
        page.getByTestId('resource-list-pager').locator('[aria-current="page"]'),
      ).toHaveText('2');
    } finally {
      await context.close();
    }
  });

  // This was a dead link in both directions: the sort header wrote
  // ?sort=<key>&dir=asc|desc, and the scenario route validated `dir` as its
  // text-direction axis and 404'd on anything else. The axis moved to ?text=
  // because SortURL hardcodes `dir` for every table in the product, so the
  // dev-only axis was the thing that had to yield.
  test('a scenario column sort link navigates', async ({ browser }) => {
    const { page, context } = await noScript(browser, { persona: 'admin' });
    try {
      await page.goto('/dev/scenarios/resource-list');
      const sort = page.locator('[data-ui="data-table"] [data-ui="column-header"] a').first();
      const answered = page.waitForResponse(
        (r) => r.url().includes('sort=') && r.request().resourceType() === 'document',
      );
      await sort.click();
      expect((await answered).status()).toBe(200);
    } finally {
      await context.close();
    }
  });

  test('carousel dots scroll to their slide', async ({ browser }) => {
    // Narrow enough that the track actually overflows; at desktop width all
    // three slides fit and there is no scrolling to observe either way.
    const { page, context } = await noScript(browser, { viewport: CAROUSEL_VIEWPORT });
    try {
      await page.goto('/dev/gallery');
      const track = page.locator('[data-carousel-track]');
      expect(await track.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
      const dots = page.locator('[data-ui="carousel-dots"] a');
      await expect(dots.nth(2)).toHaveAttribute('href', '#slide-widgets');
      await dots.nth(2).click();
      expect(new URL(page.url()).hash).toBe('#slide-widgets');
      // The browser's own fragment navigation scrolls the snap container. The
      // controller only keeps aria-current in step; it is not what moves the
      // slides, which is why the dots work with nothing loaded.
      await expect
        .poll(() => track.evaluate((el) => el.scrollLeft), { timeout: 5000 })
        .toBeGreaterThan(0);
      await expect(page.locator('#slide-widgets')).toBeInViewport();
    } finally {
      await context.close();
    }
  });
});

test.describe('no script — widgets', () => {
  test('the date picker exposes a native, named, submittable date input', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const form = page.getByTestId('dev-calendar-form');
      const input = form.locator('[data-ui="date-picker"] input');
      await expect(input).toBeVisible();
      await expect(input).toHaveAttribute('type', 'date');
      await expect(input).toHaveAttribute('name', 'dev_date');
      // The bounds are on the control, so the platform enforces them before the
      // server does; the calendar is not the thing that knows the range.
      await expect(input).toHaveAttribute('min', /\d{4}-\d{2}-\d{2}/);
      await expect(input).toHaveAttribute('max', /\d{4}-\d{2}-\d{2}/);
      // Cally's trigger and popover are hidden, not inert: with no engine there
      // is no calendar to open, so there is no button offering to open one.
      await expect(form.locator('[data-calendar-trigger]')).toBeHidden();
      await expect(form.locator('[data-calendar-popover]')).toBeHidden();

      await input.fill('2026-03-04');
      const posted = page.waitForRequest((r) => r.url().includes('/dev/ui/calendar/select'));
      await form.getByRole('button', { name: 'Use this date' }).click();
      const request = await posted;
      expect(request.method()).toBe('POST');
      expect(request.postData()).toContain('dev_date=2026-03-04');
    } finally {
      await context.close();
    }
  });

  test('the date range picker exposes two native date inputs', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const range = page.locator('[data-ui="date-range-picker"]');
      const inputs = range.locator('input[type="date"]');
      await expect(inputs).toHaveCount(2);
      await expect(inputs.nth(0)).toHaveAttribute('name', 'window_start');
      await expect(inputs.nth(1)).toHaveAttribute('name', 'window_end');
      await expect(range.locator('[data-calendar-trigger]')).toBeHidden();
    } finally {
      await context.close();
    }
  });

  test('the markdown editor is a plain textarea that submits', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const form = page.getByTestId('dev-editor-form');
      const textarea = form.getByTestId('dev-editor').locator('textarea');
      await expect(textarea).toBeVisible();
      await expect(textarea).toHaveAttribute('name', 'dev_body_md');

      // The formatting toolbar does nothing without uiMarkdownEditor, so it is
      // hidden until the controller reveals it.
      await expect(form.locator('[data-ui="editor-toolbar"]')).toBeHidden();

      // Where a length limit is declared it lives on the control itself, so the
      // limit still holds when the live counter never runs. Checked before the
      // submit below, which is a real navigation away from this page.
      await expect(
        page.locator('[data-editor-name="release_notes"] textarea'),
      ).toHaveAttribute('maxlength', /\d+/);

      await textarea.fill('# Heading\n\nplain markdown');
      const posted = page.waitForRequest((r) => r.url().includes('/dev/ui/editor/preview'));
      await form.getByRole('button', { name: 'Update preview' }).click();
      // Form encoding writes a space as "+", which decodeURIComponent leaves
      // alone - so the body has to be normalised before it is matched.
      const body = (await posted).postData() ?? '';
      expect(decodeURIComponent(body.replace(/\+/g, ' '))).toContain('plain markdown');
    } finally {
      await context.close();
    }
  });

  test('the media picker is hidden rather than inert', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      await expect(page.locator('[data-ui="media-picker"]').first()).toBeHidden();
    } finally {
      await context.close();
    }
  });

  test('the file dropzone wraps a real file input', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const zone = page.locator('[data-ui="file-dropzone"]');
      // A label around a real input: the pointer path is the label click and
      // the keyboard path is the input itself, neither of which is the drop
      // gesture the controller adds.
      await expect(zone).toHaveJSProperty('tagName', 'LABEL');
      const input = zone.locator('input[type="file"]');
      await expect(input).toHaveAttribute('name', 'attachments');
      await expect(input).toHaveCount(1);
      await expect(zone).toHaveAttribute('for', await input.getAttribute('id') ?? '');
    } finally {
      await context.close();
    }
  });

  test('the data grid falls back to a semantic table with sorting and paging', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const grid = page.locator('[data-ui="data-grid"]');
      const table = grid.locator('table');
      await expect(table).toBeVisible();
      await expect(table.locator('caption')).not.toHaveText('');
      await expect(table.locator('th[scope="col"]').first()).toBeVisible();
      await expect(table.locator('tbody tr')).not.toHaveCount(0);
      // Windowed rows must never be the only way to reach the data, so the
      // pager stays even though the enhancement scrolls.
      const pager = grid.locator('[data-ui="pagination"] a').first();
      await expect(pager).toHaveAttribute('href', /page=/);
      await expect(grid.locator('[data-ui="column-header"] a').first()).toHaveAttribute(
        'href',
        /sort=/,
      );
    } finally {
      await context.close();
    }
  });

  test('a kanban card offers a usable move control', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      const card = page.locator('[data-ui="kanban-card"]').first();
      await expect(card.getByRole('button', { name: /^Move to/ }).first()).toBeVisible();
    } finally {
      await context.close();
    }
  });
});

// Every chart is a figure whose table is rendered first and never hidden. The
// canvas is the enhancement, and it stays hidden until Chart.js constructs
// successfully - so a chart with no engine is a complete, readable table rather
// than an empty box.
const CHARTS = ['bar-chart', 'line-chart', 'area-chart', 'donut-chart', 'sparkline'] as const;

test.describe('no script — charts', () => {
  for (const name of CHARTS) {
    test(`${name} renders a caption, a summary and a data table with no canvas`, async ({
      browser,
    }) => {
      const { page, context } = await noScript(browser);
      try {
        await page.goto('/dev/gallery');
        const figure = page.locator(`[data-ui="${name}"]`).first();
        await expect(figure).toBeVisible();

        // figcaption carries the title and the summary, and both are wired as
        // the figure's accessible name and description - so the point of the
        // picture is stated in text, not only drawn.
        const labelledBy = await figure.getAttribute('aria-labelledby');
        const describedBy = await figure.getAttribute('aria-describedby');
        await expect(page.locator(`#${labelledBy}`)).not.toHaveText('');
        await expect(page.locator(`#${describedBy}`)).not.toHaveText('');

        const table = figure.locator('table');
        await expect(table).toBeVisible();
        await expect(table.locator('caption')).not.toHaveText('');
        await expect(table.locator('thead th[scope="col"]')).not.toHaveCount(0);
        const rows = table.locator('tbody tr');
        await expect(rows).not.toHaveCount(0);
        // Each row names its point and carries its value, which is what makes
        // the table the data source the adapter parses rather than a summary.
        await expect(rows.first().locator('th[scope="row"]')).not.toHaveText('');
        await expect(rows.first().locator('td[data-value]').first()).not.toHaveText('');

        // The mount is hidden, so no canvas is required to read the chart.
        await expect(figure.locator('[data-chart-mount]')).toBeHidden();
        await expect(figure.locator('canvas')).toBeHidden();
      } finally {
        await context.close();
      }
    });
  }
});

test.describe('no script — script-only controls stay hidden', () => {
  test('controls that cannot act without their engine are hidden', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      // One test for the whole rule: a control whose only behaviour comes from
      // a controller must be absent from the page, not present and dead. Each
      // of these is revealed by its own controller on init.
      for (const selector of [
        '[data-chart-mount]',
        '[data-calendar-trigger]',
        '[data-calendar-popover]',
        '[data-ui="editor-toolbar"]',
        '[data-ui="column-picker"]',
      ]) {
        const controls = page.locator(selector);
        const count = await controls.count();
        expect(count, `${selector} renders nothing to check`).toBeGreaterThan(0);
        for (let i = 0; i < count; i++) {
          await expect(controls.nth(i), selector).toBeHidden();
        }
      }
    } finally {
      await context.close();
    }
  });

  test('the hover card keeps a visible, named trigger', async ({ browser }) => {
    const { page, context } = await noScript(browser);
    try {
      await page.goto('/dev/gallery');
      // Every instance the page renders, each scoped to its own root, with the
      // count read from the DOM rather than a literal list of names. The
      // catalog renders two today so that a cross-wired controller is visible;
      // a hardcoded list silently stops covering a third, which is the same
      // drift that made this test ambiguous when the second one arrived.
      const cards = page.locator('[data-ui="hover-card"]');
      const count = await cards.count();
      expect(count, 'no hover card renders here').toBeGreaterThan(0);
      for (let i = 0; i < count; i++) {
        const card = cards.nth(i);
        // The declared fallback is the trigger itself: the preview is an extra,
        // and the trigger stays a named, focusable control without it. Asserted
        // through role rather than tabindex, because whether focus comes from a
        // button element or an explicit tabindex is not the contract.
        const trigger = card.getByRole('button');
        await expect(trigger).toBeVisible();
        await expect(trigger).not.toHaveAccessibleName('');
        await trigger.focus();
        await expect(trigger).toBeFocused();
        await expect(card.locator('[data-ui-hovercard-panel]')).toBeHidden();
      }
      // No panel anywhere is visible with scripts off. Per-instance assertions
      // above cannot see a panel that escaped its own root, and a preview that
      // paints without its controller is exactly what x-cloak exists to stop.
      await expect(page.locator('[data-ui="hover-card"] [data-ui-hovercard-panel]:visible')).toHaveCount(0);
    } finally {
      await context.close();
    }
  });
});

// Reduced motion needs the engines, so these run with scripting on. Each asserts
// the consequence a user would feel - nothing moves - rather than reading our
// own controller state.
test.describe('reduced motion', () => {
  async function open(
    browser: Browser,
    reducedMotion: 'reduce' | 'no-preference',
    viewport?: { width: number; height: number },
  ) {
    const context = await browser.newContext({
      reducedMotion,
      ...(viewport ? { viewport } : {}),
    });
    return { page: await context.newPage(), context };
  }

  // No installed surface passes Autoplay: true, so the server's autoplay markup
  // is substituted into the response. The rewritten attributes are exactly what
  // ui.Carousel emits for Autoplay: true with an interval, and the interval is
  // shortened so the observation window is a few seconds rather than a minute.
  async function autoplayCarousel(page: Page, intervalMS: number) {
    await page.route('**/dev/gallery', async (route) => {
      const response = await route.fetch();
      const body = (await response.text())
        .replace('data-carousel-autoplay="false"', 'data-carousel-autoplay="true"')
        .replace('data-carousel-interval="4000"', `data-carousel-interval="${intervalMS}"`);
      await route.fulfill({ response, body });
    });
    await page.goto('/dev/gallery');
    const track = page.locator('[data-carousel-track]');
    expect(await track.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
    return track;
  }

  const AUTOPLAY_INTERVAL = 1200;

  test('the carousel autoplays when motion is allowed', async ({ browser }) => {
    const { page, context } = await open(browser, 'no-preference', CAROUSEL_VIEWPORT);
    try {
      const track = await autoplayCarousel(page, AUTOPLAY_INTERVAL);
      // Control for the assertion below: without this, "did not move" would
      // also pass on a carousel that can never move.
      await expect
        .poll(() => track.evaluate((el) => el.scrollLeft), { timeout: 10_000 })
        .toBeGreaterThan(0);
    } finally {
      await context.close();
    }
  });

  test('the carousel does not autoplay under reduced motion', async ({ browser }) => {
    const { page, context } = await open(browser, 'reduce', CAROUSEL_VIEWPORT);
    try {
      const track = await autoplayCarousel(page, AUTOPLAY_INTERVAL);
      // Proving an absence needs a window rather than a condition to wait for.
      // Three intervals is long enough that the control test above has already
      // advanced twice by this point.
      await page.waitForTimeout(AUTOPLAY_INTERVAL * 3);
      expect(await track.evaluate((el) => el.scrollLeft)).toBe(0);
      // aria-current is the position a screen reader reports, and it must not
      // have drifted either.
      await expect(page.locator('[data-ui="carousel-dots"] a').first()).toHaveAttribute(
        'aria-current',
        'true',
      );
    } finally {
      await context.close();
    }
  });

  // Chart.js owns the animation, so its own public registry is the honest place
  // to read the resolved setting; there is no DOM state that reports it.
  async function chartAnimation(page: Page): Promise<unknown> {
    await page.goto('/dev/gallery');
    await page.waitForFunction(() => {
      const runtime = window as unknown as {
        Chart?: { getChart(node: Element): { options: { animation: unknown } } | undefined };
      };
      const canvas = document.querySelector('[data-ui="bar-chart"] [data-chart-canvas]');
      return Boolean(runtime.Chart && canvas && runtime.Chart.getChart(canvas));
    });
    // The visible consequence of a successful init: the mount is revealed, so
    // the assertion below is about a chart that really drew.
    await expect(page.locator('[data-ui="bar-chart"] [data-chart-mount]')).toBeVisible();
    return page.evaluate(() => {
      const runtime = window as unknown as {
        Chart: { getChart(node: Element): { options: { animation: unknown } } };
      };
      const canvas = document.querySelector('[data-ui="bar-chart"] [data-chart-canvas]');
      return runtime.Chart.getChart(canvas as Element).options.animation;
    });
  }

  test('chart animation is disabled under reduced motion', async ({ browser }) => {
    const { page, context } = await open(browser, 'reduce');
    try {
      expect(await chartAnimation(page)).toBe(false);
    } finally {
      await context.close();
    }
  });

  test('charts animate when motion is allowed', async ({ browser }) => {
    const { page, context } = await open(browser, 'no-preference');
    try {
      expect(await chartAnimation(page)).not.toBe(false);
    } finally {
      await context.close();
    }
  });

  // Same reasoning as the chart: SortableJS resolves the option internally, and
  // Sortable.get is its documented accessor for the instance on an element.
  async function kanbanAnimation(page: Page): Promise<number> {
    await page.goto('/dev/gallery');
    await page.waitForFunction(() => {
      const runtime = window as unknown as {
        Sortable?: { get(node: Element): { options: { animation: number } } | undefined };
      };
      const list = document.querySelector('[data-ui="kanban"] [data-kanban-list]');
      return Boolean(runtime.Sortable && list && runtime.Sortable.get(list));
    });
    return page.evaluate(() => {
      const runtime = window as unknown as {
        Sortable: { get(node: Element): { options: { animation: number } } };
      };
      const list = document.querySelector('[data-ui="kanban"] [data-kanban-list]');
      return runtime.Sortable.get(list as Element).options.animation;
    });
  }

  test('kanban drag animation is zero under reduced motion', async ({ browser }) => {
    const { page, context } = await open(browser, 'reduce');
    try {
      expect(await kanbanAnimation(page)).toBe(0);
    } finally {
      await context.close();
    }
  });

  test('kanban cards animate when motion is allowed', async ({ browser }) => {
    const { page, context } = await open(browser, 'no-preference');
    try {
      expect(await kanbanAnimation(page)).toBeGreaterThan(0);
    } finally {
      await context.close();
    }
  });
});
