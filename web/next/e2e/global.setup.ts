import { test as setup, expect } from '@playwright/test'

import { STORAGE_STATE } from '../playwright.config'
import { BRIDGE_TOKEN, E2E_USER_ID, pinEnglish } from './helpers/auth'

/**
 * One-time bridge login -> persisted storageState (session cookie + the
 * console's language preference). Every spec depends on this, so we hit the
 * rate-limited bridge ONCE per run instead of once per test.
 */
setup('authenticate via bridge', async ({ page }) => {
  setup.skip(
    !BRIDGE_TOKEN,
    'E2E_BRIDGE_TOKEN not set - cannot establish a session.',
  )

  const res = await page.request.post(
    `/api/v2/bridge/exchange?token=${encodeURIComponent(BRIDGE_TOKEN)}&user_id=${E2E_USER_ID}`,
  )
  expect(
    res.status(),
    `bridge exchange should return 200 (got ${res.status()}: ${await res.text()})`,
  ).toBe(200)

  // The console's own session probe: if this is not 200 every UI spec would be
  // bounced to /login, so fail here with a clear message instead.
  const me = await page.request.get('/api/v2/~/user/me')
  expect(
    me.status(),
    `the bridge session must satisfy /api/v2/~/user/me (got ${me.status()})`,
  ).toBe(200)

  // The console authenticates from the session cookie alone (it reads
  // /api/v2/~/user/me), so nothing but the language preference is seeded into
  // localStorage. addInitScript (not goto-then-evaluate) guarantees the key
  // exists before any app script runs, and the visit below makes the origin
  // present in the saved storageState.
  await pinEnglish(page)
  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')

  await page.context().storageState({ path: STORAGE_STATE })
})
