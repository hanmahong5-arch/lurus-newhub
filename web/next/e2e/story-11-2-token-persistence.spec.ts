import { expect, test } from '@playwright/test'

import {
  BRIDGE_SKIP,
  BRIDGE_TOKEN,
  gotoDashboard,
  loginViaBridge,
} from './helpers/auth'
import { createKeyViaApi, deleteKeysNamed, keyRow } from './helpers/keys'

/**
 * Story 11-2 DoD e2e:
 *   "log in via the bridge -> land on the dashboard -> create a key -> see it
 *    in the key list -> log out -> log back in -> the key is still there."
 *
 * New-console contracts asserted on top of the original flow:
 *   - with the session cookie dropped, the console does NOT render the key
 *     list: it bounces to the sign-in route and carries the page it was on in
 *     `?redirect=` (so the person lands back on /keys after signing in);
 *   - the keys belong to the account, not to the browser session.
 *
 * Fully local-testable (no external provider needed).
 */
test.describe('Story 11-2 - key persistence across re-login', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP)

  const keyName = `e2e-persistence-${Date.now()}`

  test('create -> relogin -> key persists', async ({ page }) => {
    // Step 1: shared session lands on the dashboard.
    await gotoDashboard(page)

    // Step 2: create a key via API (faster than the UI form; the UI create
    // path is covered by smoke-login-token.spec.ts).
    await createKeyViaApi(page, keyName)

    // Step 3: confirm in the Keys page UI.
    await page.goto('/keys')
    await expect(keyRow(page, keyName).first()).toBeVisible({
      timeout: 15_000,
    })

    // Step 4: log out (drop the session cookie). The console must refuse to
    // show the list and send the browser to sign-in, remembering where it was.
    await page.context().clearCookies()
    await page.goto('/keys')
    await expect(page).toHaveURL(/\/login/, { timeout: 15_000 })
    expect(new URL(page.url()).searchParams.get('redirect')).toContain('/keys')
    await expect(keyRow(page, keyName)).toHaveCount(0)

    // Step 5: log back in via the bridge.
    await loginViaBridge(page)
    await gotoDashboard(page)

    // Step 6: the key is still present after re-login.
    await page.goto('/keys')
    await expect(keyRow(page, keyName).first()).toBeVisible({
      timeout: 15_000,
    })
  })

  test.afterEach(async ({ page }) => {
    // The context may have lost its session mid-test; restore it for cleanup.
    const probe = await page.request.get('/api/v2/~/user/me')
    if (!probe.ok()) await loginViaBridge(page)
    await deleteKeysNamed(page, keyName)
  })
})
