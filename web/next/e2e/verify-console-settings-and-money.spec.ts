import { expect, test, type Page, type Route } from '@playwright/test'

import { BRIDGE_SKIP, BRIDGE_TOKEN } from './helpers/auth'

/**
 * Functional verification for the console-settings and money surfaces.
 *
 * Everything these cover is asserted in jsdom unit tests too. The point here is
 * what a unit test structurally cannot reach: the real GET /api/option/
 * payload, the real save round trip, real layout/formatting, and what the
 * operator actually ends up looking at.
 *
 * The UI runs in English (the setup project pins the console language), so
 * labels are the English ones.
 *
 * What changed against the retired console (new contracts, asserted below):
 *   - The dashboard-panel settings live at /admin/settings/panels and edit the
 *     FAQ as a JSON document (field "FAQ", save button "Save Changes"), not as
 *     an "Add FAQ" dialog with per-entry rows.
 *   - The pre-hydration write guard is now structural: the form is not rendered
 *     at all until GET /api/option/ has answered (a "Loading settings..."
 *     placeholder stands in), so there is nothing to save before the read.
 *   - "Save Changes" with nothing edited writes nothing ("No changes to save").
 *   - The billing page is /wallet; its history reads come from
 *     /api/v2/<tenant>/billing/topups and /api/wallet/info.
 */

const FAQ_KEY = 'console_setting.faq'
const FAQ_ENABLED_KEY = 'console_setting.faq_enabled'

/** Read an option straight from the server, bypassing the SPA entirely. */
async function readOption(
  page: Page,
  key: string,
): Promise<string | undefined> {
  const res = await page.request.get('/api/option/')
  expect(res.status()).toBe(200)
  const body = await res.json()
  return ((body?.data ?? []) as Array<{ key: string; value: string }>).find(
    (o) => o.key === key,
  )?.value
}

async function writeOption(page: Page, key: string, value: string | boolean) {
  const res = await page.request.put('/api/option/', { data: { key, value } })
  expect(res.status()).toBe(200)
}

async function gotoPanelsSettings(page: Page) {
  await page.goto('/admin/settings/panels')
  await expect(faqEditor(page)).toBeVisible({ timeout: 20_000 })
}

/** The FAQ field's JSON editor (a textarea named after the field label). */
function faqEditor(page: Page) {
  return page.getByRole('textbox', { name: 'FAQ', exact: true })
}

function saveButton(page: Page) {
  return page.getByRole('button', { name: 'Save Changes' })
}

/** Stored FAQ entries as the server holds them right now. */
async function storedFaq(page: Page): Promise<Array<Record<string, unknown>>> {
  return JSON.parse((await readOption(page, FAQ_KEY)) || '[]')
}

test.describe('console settings - the pre-hydration write guard', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP)

  // Each test starts from the fresh-install shape the server ships (an empty
  // FAQ blob, panel enabled) and puts back whatever it found.
  let originalFaq = ''
  let originalEnabled = 'true'

  test.beforeEach(async ({ page }) => {
    originalFaq = (await readOption(page, FAQ_KEY)) ?? ''
    originalEnabled = (await readOption(page, FAQ_ENABLED_KEY)) ?? 'true'
    await writeOption(page, FAQ_KEY, '')
    await writeOption(page, FAQ_ENABLED_KEY, true)
  })

  test.afterEach(async ({ page }) => {
    await writeOption(page, FAQ_KEY, originalFaq)
    await writeOption(page, FAQ_ENABLED_KEY, originalEnabled === 'true')
  })

  test('the server serves an empty blob beside a real enable flag', async ({
    page,
  }) => {
    // The premise the whole guard rests on. If this changes, so must the guard.
    expect(await readOption(page, FAQ_KEY)).toBe('')
    expect(await readOption(page, FAQ_ENABLED_KEY)).toMatch(/^(true|false)$/)
  })

  test('no editable form or save button exists before the options are read', async ({
    page,
  }) => {
    await writeOption(
      page,
      FAQ_KEY,
      JSON.stringify([{ id: 1, question: 'pre-existing-q', answer: 'a' }]),
    )

    // Hold the options read open: this is "the panel has not hydrated yet".
    let release: () => void = () => {}
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    // Every held request is tracked: unrouting while a handler still waits on
    // the gate lets Playwright continue the request itself, and the handler's
    // own continue() then throws "Route is already handled".
    const held: Promise<void>[] = []
    const holdReads = (route: Route) => {
      const handled = (async () => {
        if (route.request().method() === 'GET') await gate
        await route.continue()
      })()
      held.push(handled)
      return handled
    }
    await page.route('**/api/option/', holdReads)
    try {
      await page.goto('/admin/settings/panels')
      await expect(
        page.getByRole('status', { name: 'Loading settings...' }),
      ).toBeVisible({ timeout: 20_000 })
      // Nothing to type into and nothing to save while unread: the write the
      // guard exists to prevent is not reachable.
      await expect(faqEditor(page)).toHaveCount(0)
      await expect(saveButton(page)).toHaveCount(0)
    } finally {
      release()
      await Promise.all(held)
      await page.unroute('**/api/option/', holdReads)
    }

    // Once read, the editor carries the STORED list, not a blank draft.
    await expect(faqEditor(page)).toBeVisible({ timeout: 20_000 })
    await expect(faqEditor(page)).toHaveValue(/pre-existing-q/)
  })

  test('adds and saves the first entry on a fresh install', async ({
    page,
  }) => {
    await gotoPanelsSettings(page)

    await faqEditor(page).fill(
      JSON.stringify([
        { id: 1, question: 'local-verify-question', answer: 'local-verify-answer' },
      ]),
    )

    // The brick check: the save must be reachable on a fresh install, where
    // the server answers the FAQ with "" (genuinely empty, not "loading").
    await expect(saveButton(page)).toBeEnabled()
    await saveButton(page).click()

    await expect
      .poll(async () => await readOption(page, FAQ_KEY), { timeout: 20_000 })
      .toContain('local-verify-question')
  })

  test('a stored list survives a save from a freshly loaded panel', async ({
    page,
  }) => {
    // The defect this guards: the editor writes the whole blob back, so saving
    // before it has read replaces every stored entry with local state. Here it
    // HAS read, so the existing entry must survive a save that adds a second.
    await writeOption(
      page,
      FAQ_KEY,
      JSON.stringify([{ id: 1, question: 'pre-existing-q', answer: 'a' }]),
    )
    await gotoPanelsSettings(page)
    await expect(faqEditor(page)).toHaveValue(/pre-existing-q/, {
      timeout: 20_000,
    })

    const shown = JSON.parse(await faqEditor(page).inputValue()) as Array<
      Record<string, unknown>
    >
    await faqEditor(page).fill(
      JSON.stringify([...shown, { id: 2, question: 'second-q', answer: 'b' }]),
    )
    await saveButton(page).click()

    await expect
      .poll(async () => await readOption(page, FAQ_KEY), { timeout: 20_000 })
      .toContain('second-q')

    const stored = await readOption(page, FAQ_KEY)
    expect(stored, 'the pre-existing entry must not be dropped').toContain(
      'pre-existing-q',
    )
    expect(JSON.parse(stored as string)).toHaveLength(2)
  })

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
    )
    await gotoPanelsSettings(page)
    await expect(faqEditor(page)).toHaveValue(/zero-id-q/, { timeout: 20_000 })

    const shown = JSON.parse(await faqEditor(page).inputValue()) as Array<
      Record<string, unknown>
    >
    await faqEditor(page).fill(
      JSON.stringify([...shown, { id: 2, question: 'trigger-save-q', answer: 'c' }]),
    )
    await saveButton(page).click()

    await expect
      .poll(async () => await readOption(page, FAQ_KEY), { timeout: 20_000 })
      .toContain('trigger-save-q')

    const ids = (await storedFaq(page)).map((r) => r.id)
    expect(ids, 'stored ids must be preserved as they were').toEqual([0, 1, 2])
  })

  test('saving without editing writes nothing', async ({ page }) => {
    const before = JSON.stringify([
      { id: 1, question: 'untouched-q', answer: 'a' },
    ])
    await writeOption(page, FAQ_KEY, before)
    await gotoPanelsSettings(page)
    await expect(faqEditor(page)).toHaveValue(/untouched-q/, {
      timeout: 20_000,
    })

    await saveButton(page).click()
    await expect(page.getByText('No changes to save').first()).toBeVisible()

    expect(await readOption(page, FAQ_KEY)).toBe(before)
  })
})

test.describe('wallet page fetches once', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP)

  test('a customer with no billing history does not spin the API', async ({
    page,
  }) => {
    // The section effects were guarded on "is the data still absent?", which is
    // true again the instant a fetch resolves empty - so the effect re-fired
    // without bound. The trigger is not a failure: a customer who has never
    // topped up gets a successful 200 carrying a null balance and an empty
    // items array, which re-arms the guard exactly the same way.
    //
    // This is the regression guard for that. The unit suite cannot host it:
    // jsdom with a mocked, already-resolved API does not reproduce the re-fire.
    // Measured on the retired console before the fix: 670 requests in 5s with
    // the panel stuck on "loading...". After: 0 in the same window.
    const counts = new Map<string, number>()
    page.on('request', (r) => {
      const u = new URL(r.url()).pathname
      if (u.startsWith('/api')) counts.set(u, (counts.get(u) ?? 0) + 1)
    })

    const json = (body: unknown) => (route: Route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
    await page.route(
      /\/api\/wallet\/info(\?|$)/,
      json({
        success: true,
        data: {
          source: 'platform',
          balance: null,
          frozen: null,
          available: null,
          lifetime_topup: null,
          lifetime_spend: null,
          pending_orders: null,
        },
      }),
    )
    await page.route(
      /\/api\/v2\/[^/]+\/billing\/topups/,
      json({ success: true, data: { items: [], total: 0 } }),
    )

    await page.goto('/wallet')
    // It must actually leave the loading state: the stats and the empty
    // history both render.
    await expect(page.getByTestId('stat-wallet')).toContainText(
      'Wallet balance',
      { timeout: 20_000 },
    )
    await expect(page.getByText('No top-ups yet')).toBeVisible()

    // Let it settle, then measure a clean window.
    await page.waitForTimeout(1500)
    counts.clear()
    await page.waitForTimeout(4000)

    const walletCalls = [...counts.entries()].find(([path]) =>
      path.endsWith('/api/wallet/info'),
    )
    expect(
      walletCalls?.[1] ?? 0,
      'the wallet read must not be re-fetched once it has answered',
    ).toBeLessThanOrEqual(1)
    const topupCalls = [...counts.entries()].find(([path]) =>
      path.endsWith('/billing/topups'),
    )
    expect(
      topupCalls?.[1] ?? 0,
      'the top-up history must not be re-fetched once it has answered',
    ).toBeLessThanOrEqual(1)
    // And no endpoint at all may be hammered: a real poll is a handful of
    // requests in this window, a spin is hundreds.
    const hottest = Math.max(0, ...counts.values())
    expect(hottest, `request counts in the window: ${[...counts]}`).toBeLessThan(
      4,
    )
  })
})

/** "$-0.00", "-$0.00", "¥-0.00", or a bare "-0.00": a signed zero on screen. */
const SIGNED_ZERO = /(?:[$¥€£]\s*-\s*0\.00|-\s*[$¥€£]\s*0\.00|(?:^|\s)-0\.00)/

function expectCleanMoney(text: string, where: string) {
  expect(text, `no NaN may reach ${where}`).not.toMatch(/\bNaN\b/)
  expect(text, `no signed zero may reach ${where}`).not.toMatch(SIGNED_ZERO)
  expect(text, `no undefined may reach ${where}`).not.toMatch(/\bundefined\b/)
}

/** Remember the most recent /user/me body the page itself read. */
function trackSelf(page: Page) {
  const seen: { used?: number; remaining?: number } = {}
  page.on('response', async (res) => {
    if (!/\/api\/v2\/[^/]+\/user\/me(\?|$)/.test(res.url())) return
    try {
      const data = (await res.json())?.data
      seen.used = data?.used_quota
      seen.remaining = data?.remaining_quota
    } catch {
      // A body that is not JSON is not a quota read.
    }
  })
  return seen
}

test.describe('money on screen', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP)

  test('the user table renders real figures, not NaN or a signed zero', async ({
    page,
  }) => {
    await page.goto('/admin/users')
    const table = page.getByRole('table').first()
    await expect(table).toBeVisible({ timeout: 25_000 })
    await expect(page.getByRole('row').nth(1)).toBeVisible()

    expectCleanMoney((await table.innerText()) || '', 'a money column')
  })

  test('the server publishes the exchange rate the console renders with', async ({
    page,
  }) => {
    // The defect: two renderers fell back to different CNY rates, so one amount
    // read differently in two columns of the same row. Both now key on this.
    const res = await page.request.get('/api/status')
    expect(res.status()).toBe(200)
    const status = (await res.json())?.data ?? {}
    expect(
      status.usd_exchange_rate,
      'the server must publish a rate for the console to use',
    ).toBeGreaterThan(0)
  })

  test('dashboard, wallet and usage logs agree on the same money', async ({
    page,
  }) => {
    const self = trackSelf(page)
    const value = (testId: string) =>
      page.getByTestId(testId).locator('[title]').first()

    // Dashboard: balance and total spent.
    await page.goto('/dashboard')
    await expect(page.getByTestId('kpi-balance')).toBeVisible({
      timeout: 20_000,
    })
    await expect(value('kpi-balance')).toBeVisible({ timeout: 20_000 })
    await expect(value('kpi-spent')).toBeVisible()
    const balance = (await value('kpi-balance').innerText()).trim()
    const spent = (await value('kpi-spent').innerText()).trim()
    await expect.poll(() => self.used, { timeout: 10_000 }).toBeDefined()
    const dashboardRead = { ...self }
    expect(balance, 'the balance is a real amount').not.toBe('--')
    expect(spent, 'the spend is a real amount').not.toBe('--')
    expectCleanMoney(
      await page.getByTestId('kpi-balance').innerText(),
      'the balance card',
    )
    expectCleanMoney(
      await page.getByTestId('kpi-spent').innerText(),
      'the spend card',
    )

    // Wallet: the same user, read through a different page.
    delete self.used
    delete self.remaining
    await page.goto('/wallet')
    const stats = page.getByTestId('wallet-stats')
    await expect(stats).toBeVisible({ timeout: 20_000 })
    // Stats render before every read has answered: "--" = user read pending,
    // "..." = wallet read pending. Wait for both before comparing. (A wallet
    // that answers with an error also shows "--"; that is not asserted on.)
    const walletValue = (testId: string) =>
      page.getByTestId(testId).locator('.font-mono')
    await expect(walletValue('stat-usage')).not.toHaveText('--', {
      timeout: 20_000,
    })
    await expect(walletValue('stat-wallet')).not.toHaveText('...', {
      timeout: 20_000,
    })
    await expect.poll(() => self.used, { timeout: 10_000 }).toBeDefined()
    expectCleanMoney(await stats.innerText(), 'the wallet stats')
    const walletUsage = (await walletValue('stat-usage').innerText()).trim()
    const walletRead = { ...self }

    if (
      dashboardRead.used !== walletRead.used ||
      dashboardRead.remaining !== walletRead.remaining
    ) {
      // Real traffic moved the account between the two page loads; comparing
      // would be comparing two moments, not two renderers.
      test.info().annotations.push({
        type: 'note',
        description: 'quota moved between the two reads; equality not asserted',
      })
    } else {
      expect(walletUsage, 'total usage: wallet and dashboard agree').toBe(spent)

      const walletLabel = await page
        .getByTestId('stat-wallet')
        .innerText()
      if (/quota balance/i.test(walletLabel)) {
        // A sign-in with no linked platform wallet shows its console quota,
        // by the same field and rule as the dashboard balance.
        const walletBalance = (await walletValue('stat-wallet').innerText()).trim()
        expect(walletBalance, 'balance: wallet and dashboard agree').toBe(
          balance,
        )
      } else {
        // A linked platform wallet is genuinely CNY, a different currency from
        // the console quota - equality does not apply.
        test.info().annotations.push({
          type: 'note',
          description:
            'wallet is a linked platform wallet (CNY); balance equality not asserted',
        })
      }
    }

    // Usage logs: the statistics header renders real figures.
    await page.goto('/usage-logs')
    const header = page.getByTestId('log-stat-header')
    await expect(header).toBeVisible({ timeout: 20_000 })
    await expect(header.getByRole('alert')).toHaveCount(0)
    expectCleanMoney(await header.innerText(), 'the usage-log statistics')
  })
})
