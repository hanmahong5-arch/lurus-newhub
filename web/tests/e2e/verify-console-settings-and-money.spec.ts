import { test, expect, type Page } from '@playwright/test';

/**
 * Functional verification for the console-settings and money changes.
 *
 * Everything these cover was previously asserted only in jsdom unit tests. The
 * point here is what a unit test structurally cannot reach: the real
 * GET /api/option/ payload, the real save round-trip, and what the operator
 * actually ends up looking at.
 *
 * The most important case is `adds and saves the first entry on a fresh
 * install`. The panels refuse to write a collection they have never read, so a
 * pre-hydration save cannot wipe the stored list. But the server answers
 * console_setting.faq with "" on a fresh install — genuinely empty, not "still
 * loading" — so a guard keyed on the blob alone would refuse forever and the
 * console could never be configured at all. That failure is invisible to a test
 * that only checks the guard blocks.
 *
 * The UI runs under Playwright's default en-US locale, so labels here are the
 * English ones. Panels are scoped through their own toolbar: three panels share
 * this tab and each renders its own identically-named "Save Settings".
 */

const FAQ_KEY = 'console_setting.faq';

async function gotoContentSettings(page: Page) {
  await page.goto('/console/setting?tab=content');
  await page.waitForLoadState('networkidle');
}

/** The FAQ panel's own toolbar — the one ancestor holding exactly one save. */
function faqToolbar(page: Page) {
  return page
    .locator('div.flex.gap-2')
    .filter({ has: page.getByRole('button', { name: 'Add FAQ' }) })
    .first();
}

function faqSave(page: Page) {
  return faqToolbar(page).getByRole('button', { name: 'Save Settings' });
}

async function addFaqEntry(page: Page, question: string, answer: string) {
  await page.getByRole('button', { name: 'Add FAQ' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add FAQ' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox', { name: /Question Title/ }).fill(question);
  await dialog.getByRole('textbox', { name: /Answer Content/ }).fill(answer);
  // The confirm button reads "Save", but Semi sets aria-label="confirm", which
  // overrides the visible text for accessible-name matching.
  await dialog.getByRole('button', { name: 'confirm' }).click();
  await expect(dialog).toBeHidden();
}

/** Read an option straight from the server, bypassing the SPA entirely. */
async function readOption(
  page: Page,
  key: string,
): Promise<string | undefined> {
  const res = await page.request.get('/api/option/');
  expect(res.status()).toBe(200);
  const body = await res.json();
  return ((body?.data ?? []) as any[]).find((o) => o.key === key)?.value;
}

async function writeOption(page: Page, key: string, value: string) {
  const res = await page.request.put('/api/option/', { data: { key, value } });
  expect(res.status()).toBe(200);
}

test.describe('console settings — the pre-hydration write guard', () => {
  test.beforeEach(async ({ page }) => {
    // Put the panel back to the fresh-install shape the server ships.
    await writeOption(page, FAQ_KEY, '');
  });

  test('the server serves an empty blob beside a real enable flag', async ({
    page,
  }) => {
    // The premise the whole guard rests on. If this changes, so must the guard.
    expect(await readOption(page, FAQ_KEY)).toBe('');
    expect(await readOption(page, 'console_setting.faq_enabled')).toMatch(
      /^(true|false)$/,
    );
  });

  test('adds and saves the first entry on a fresh install', async ({
    page,
  }) => {
    await gotoContentSettings(page);
    await expect(page.getByRole('button', { name: 'Add FAQ' })).toBeVisible({
      timeout: 20_000,
    });

    await addFaqEntry(page, 'local-verify-question', 'local-verify-answer');

    // The brick check: the save must be reachable on a fresh install.
    await expect(faqSave(page)).toBeEnabled();
    await faqSave(page).click();

    await expect
      .poll(async () => await readOption(page, FAQ_KEY), { timeout: 20_000 })
      .toContain('local-verify-question');
  });

  test('a stored list survives a save from a freshly loaded panel', async ({
    page,
  }) => {
    // The defect this guards: the panel writes the whole blob back, so saving
    // before it has read replaces every stored entry with local state. Here it
    // HAS read, so the existing entry must survive a save that adds a second.
    await writeOption(
      page,
      FAQ_KEY,
      JSON.stringify([{ id: 1, question: 'pre-existing-q', answer: 'a' }]),
    );
    await gotoContentSettings(page);
    await expect(page.getByText('pre-existing-q').first()).toBeVisible({
      timeout: 20_000,
    });

    await addFaqEntry(page, 'second-q', 'b');
    await faqSave(page).click();

    await expect
      .poll(async () => await readOption(page, FAQ_KEY), { timeout: 20_000 })
      .toContain('second-q');

    const stored = await readOption(page, FAQ_KEY);
    expect(stored, 'the pre-existing entry must not be dropped').toContain(
      'pre-existing-q',
    );
    expect(JSON.parse(stored as string)).toHaveLength(2);
  });

  test('a stored id of 0 survives a load/save round trip', async ({ page }) => {
    // Renumbering a stored id makes every later edit and delete address the
    // wrong row. Zero is the case that used to be renumbered, being falsy.
    await writeOption(
      page,
      FAQ_KEY,
      JSON.stringify([
        { id: 0, question: 'zero-id-q', answer: 'a' },
        { id: 1, question: 'one-id-q', answer: 'b' },
      ]),
    );
    await gotoContentSettings(page);
    await expect(page.getByText('zero-id-q').first()).toBeVisible({
      timeout: 20_000,
    });

    await addFaqEntry(page, 'trigger-save-q', 'c');
    await faqSave(page).click();

    await expect
      .poll(async () => await readOption(page, FAQ_KEY), { timeout: 20_000 })
      .toContain('trigger-save-q');

    const ids = JSON.parse((await readOption(page, FAQ_KEY))!).map(
      (r: any) => r.id,
    );
    expect(ids, 'stored id 0 must be preserved').toContain(0);
    expect(new Set(ids).size, 'ids must stay distinct').toBe(ids.length);
  });
});

test.describe('settings sections fetch once', () => {
  test('a customer with no billing history does not spin the API', async ({
    page,
  }) => {
    // The section effects were guarded on "is the data still absent?", which is
    // true again the instant a fetch resolves empty — so the effect re-fired
    // without bound. The trigger is not a failure: a customer who has never
    // topped up gets a successful 200 carrying a null summary and an empty
    // items array, which re-arms the guard exactly the same way.
    //
    // This is the regression guard for that. The unit suite cannot host it:
    // jsdom with a mocked, already-resolved API does not reproduce the re-fire,
    // and this file's sibling unit test stayed green against the old code.
    // Measured here before the fix: 670 requests in 5s, panel stuck on
    // "loading…". After: 0 in the same window.
    const counts = new Map<string, number>();
    page.on('request', (r) => {
      const u = new URL(r.url()).pathname;
      if (u.startsWith('/api')) counts.set(u, (counts.get(u) ?? 0) + 1);
    });

    const empty = (body: unknown) => (route: any) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      });
    await page.route(
      '**/api/v2/user/billing/summary',
      empty({ success: true, data: null }),
    );
    await page.route(
      '**/billing/topups**',
      empty({ success: true, data: { items: [] } }),
    );

    await page.goto('/console/v2/settings');
    await page.getByText('wallet balance & usage').first().click();
    // Let it settle, then measure a clean window.
    await page.waitForTimeout(1500);
    counts.clear();
    await page.waitForTimeout(4000);

    const summaryCalls = counts.get('/api/v2/user/billing/summary') ?? 0;
    expect(
      summaryCalls,
      'the billing summary must not be re-fetched once it has answered',
    ).toBeLessThanOrEqual(1);

    // And it must actually leave the loading state.
    const panel = await page.locator('body').innerText();
    expect(panel).toContain('WALLET BALANCE');
  });
});

test.describe('money on screen', () => {
  test('the user table renders real figures, not NaN or a signed zero', async ({
    page,
  }) => {
    await page.goto('/console/user');
    await page.waitForLoadState('networkidle');
    const table = page.locator('table').first();
    await expect(table).toBeVisible({ timeout: 25_000 });

    const text = (await table.innerText()) || '';
    expect(text, 'no NaN may reach a money column').not.toMatch(/NaN/);
    expect(text, 'no signed zero may reach a money column').not.toMatch(
      /\$\s*-0\.00/,
    );
  });

  test('the server publishes the exchange rate the console renders with', async ({
    page,
  }) => {
    // The defect: two renderers fell back to different CNY rates, so one amount
    // read differently in two columns of the same row. Both now key on this.
    const res = await page.request.get('/api/status');
    expect(res.status()).toBe(200);
    const status = (await res.json())?.data ?? {};
    expect(
      status.usd_exchange_rate,
      'the server must publish a rate for the console to use',
    ).toBeGreaterThan(0);
  });
});
