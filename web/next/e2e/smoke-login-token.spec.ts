import { expect, test } from '@playwright/test'

import { BRIDGE_SKIP, BRIDGE_TOKEN, gotoDashboard } from './helpers/auth'
import {
  createKeyViaApi,
  createKeyViaUi,
  deleteKeysNamed,
  expectInsideViewport,
  keyRow,
  listKeys,
} from './helpers/keys'

/**
 * Harness smoke test - proves the stack + bridge login + tenant routing work
 * end to end through the console. Gateway for the rest of the suite.
 *
 * New-console contracts asserted here (they differ from the retired console):
 *   - the Keys page lives at /keys and creates keys in a side sheet;
 *   - the plaintext secret is shown exactly once, in the "Key created" dialog;
 *     the list only ever carries a masked key, and a reload forgets the secret;
 *   - deleting goes through a confirm dialog ("Delete this key?").
 */
test.describe('smoke - bridge login + key create/list/delete (UI)', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP)

  const keyName = `e2e-smoke-${Date.now()}`

  test('login -> dashboard -> key create/list/delete', async ({ page }) => {
    // 1. Shared session (storageState) lands on the dashboard and stays there.
    await gotoDashboard(page)

    // 2. Create a key through the UI; the secret is shown once.
    await page.goto('/keys')
    await expect(page).toHaveURL(/\/keys/)
    const secret = await createKeyViaUi(page, keyName)
    expect(secret.length, 'the issued key is a full secret').toBeGreaterThan(16)
    expect(secret, 'the issued key is not masked').not.toContain('*')

    // 3. The list (API) contains it, with a masked key - never the secret.
    const listed = (await listKeys(page)).find((k) => k.name === keyName)
    expect(listed, 'created key is listed').toBeTruthy()
    expect(listed?.key, 'the list endpoint must not reveal the secret').not.toBe(
      secret,
    )

    // 4. The table shows the row, and not the plaintext secret.
    const row = keyRow(page, keyName)
    await expect(row.first()).toBeVisible({ timeout: 15_000 })
    await expect(row.first()).not.toContainText(secret)

    // 5. Secret shown once: after a reload neither the dialog nor the secret
    //    exists anywhere on the page.
    await page.reload()
    await expect(keyRow(page, keyName).first()).toBeVisible({
      timeout: 15_000,
    })
    await expect(page.getByTestId('issued-key')).toHaveCount(0)
    expect(await page.locator('body').innerText()).not.toContain(secret)

    // 6. Delete through the row menu + confirm dialog.
    await keyRow(page, keyName)
      .first()
      .getByRole('button', { name: `More actions for ${keyName}` })
      .click()
    await page.getByRole('menuitem', { name: 'Delete key' }).click()
    await expect(page.getByText('Delete this key?')).toBeVisible()
    await page.getByRole('button', { name: 'Delete', exact: true }).click()

    await expect(keyRow(page, keyName)).toHaveCount(0, { timeout: 15_000 })
    // UI row can clear a tick before the list API reflects the delete
    // (TanStack refetch / multi-replica lag). Poll the API, don't one-shot.
    await expect
      .poll(
        async () => (await listKeys(page)).some((k) => k.name === keyName),
        { timeout: 15_000 },
      )
      .toBe(false)
  })

  test.afterEach(async ({ page }) => {
    await deleteKeysNamed(page, keyName)
  })
})

// A grid or table cannot be laid out by jsdom, so "the key actions are pushed
// off-screen on a phone" is only provable in a real browser. On a phone the
// console swaps the table for a card list (data-testid keys-mobile-list), so
// the retired detail-pane assertions (snippet, rotate, revoke) now map to the
// card, its row menu, the usage-examples dialog and the two confirm dialogs.
test.describe('keys page - phone viewport', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP)
  const WIDTH = 390
  test.use({ viewport: { width: WIDTH, height: 844 } })

  const keyName = `e2e-narrow-${Date.now()}`

  test('card, row menu, examples and confirm dialogs stay inside a 390px viewport', async ({
    page,
  }) => {
    const keyId = await createKeyViaApi(page, keyName)

    await page.goto('/keys')
    const card = page.getByTestId(`key-card-${keyId}`)
    await expect(card).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('keys-mobile-list')).toBeVisible()

    // The whole scroll body first: a single over-wide child widens every pane,
    // and the per-element checks below then fail by a fraction of a pixel with
    // no hint of the real cause.
    const overflow = await page.evaluate(() => {
      const els = [
        document.documentElement,
        document.body,
        document.querySelector('main'),
        document.querySelector('[data-testid="keys-body"]'),
      ].filter((el): el is Element => el != null)
      return Math.max(...els.map((el) => el.scrollWidth - el.clientWidth))
    })
    expect(overflow, 'page body scrolls sideways').toBeLessThanOrEqual(0)

    await expectInsideViewport(card, WIDTH, 'key card')

    // Row menu: usage examples / rotate / delete.
    const moreActions = card.getByRole('button', {
      name: `More actions for ${keyName}`,
    })
    await expectInsideViewport(moreActions, WIDTH, 'row menu button')
    await moreActions.click()
    for (const item of ['Usage examples', 'Rotate key', 'Delete key']) {
      await expectInsideViewport(
        page.getByRole('menuitem', { name: item }),
        WIDTH,
        `menu item "${item}"`,
      )
    }

    // Usage examples: the snippet block must not escape the dialog.
    await page.getByRole('menuitem', { name: 'Usage examples' }).click()
    const dialog = page.getByRole('dialog', { name: 'Usage examples' })
    await expect(dialog).toBeVisible()
    await expectInsideViewport(dialog, WIDTH, 'usage examples dialog')
    await expectInsideViewport(dialog.locator('pre'), WIDTH, 'snippet')
    await page.getByRole('button', { name: 'Done', exact: true }).click()
    await expect(dialog).toBeHidden()

    // Rotate and delete confirmations: both buttons reachable on screen.
    const confirms: Array<[string, string, string]> = [
      ['Rotate key', 'Rotate this key?', 'Rotate'],
      ['Delete key', 'Delete this key?', 'Delete'],
    ]
    for (const [menuItem, title, confirmLabel] of confirms) {
      await moreActions.click()
      await page.getByRole('menuitem', { name: menuItem }).click()
      await expect(page.getByText(title)).toBeVisible()
      await expectInsideViewport(
        page.getByRole('button', { name: confirmLabel, exact: true }),
        WIDTH,
        `"${confirmLabel}" button`,
      )
      await expectInsideViewport(
        page.getByRole('button', { name: 'Cancel', exact: true }),
        WIDTH,
        'Cancel button',
      )
      await page.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(page.getByText(title)).toBeHidden()
    }

    // The create sheet's submit button is reachable too.
    await page
      .getByRole('button', { name: 'Create key' })
      .first()
      .click()
    await expectInsideViewport(
      page.getByRole('button', { name: 'Create', exact: true }),
      WIDTH,
      'Create button',
    )
  })

  test.afterEach(async ({ page }) => {
    await deleteKeysNamed(page, keyName)
  })
})
