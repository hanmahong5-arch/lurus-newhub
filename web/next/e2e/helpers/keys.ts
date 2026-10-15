import { expect, type Locator, type Page } from '@playwright/test'

import { v2 } from './auth'

/** One entry of GET /api/v2/<slug>/tokens (only what the specs read). */
export interface KeyRow {
  id: number
  name: string
  key?: string
  [k: string]: unknown
}

/** List the first 100 keys of the session's tenant, newest first. */
export async function listKeys(page: Page): Promise<KeyRow[]> {
  const res = await page.request.get(v2('/tokens?p=1&size=100'))
  expect(res.ok(), `list keys: HTTP ${res.status()}`).toBeTruthy()
  const body = await res.json()
  return (body?.data?.items ?? body?.data?.records ?? []) as KeyRow[]
}

/** Create a key through the API; returns its id. */
export async function createKeyViaApi(
  page: Page,
  name: string,
  remainQuota = 1000,
): Promise<string> {
  const res = await page.request.post(v2('/tokens'), {
    data: { name, remain_quota: remainQuota },
  })
  expect(
    res.ok(),
    `create key: HTTP ${res.status()} ${await res.text()}`,
  ).toBeTruthy()
  const id = String((await res.json())?.data?.id ?? '')
  expect(id).toBeTruthy()
  return id
}

/**
 * Best-effort cleanup for afterEach: delete every key with exactly this name
 * (a UI-created key may exist even when the test died before it learned the id).
 */
export async function deleteKeysNamed(page: Page, name: string): Promise<void> {
  const res = await page.request.get(v2('/tokens?p=1&size=100'))
  if (!res.ok()) return
  const body = await res.json()
  const items = (body?.data?.items ?? body?.data?.records ?? []) as KeyRow[]
  for (const item of items) {
    if (item.name === name) {
      await page.request.delete(v2(`/tokens/${item.id}`))
    }
  }
}

/** The table row (desktop layout) showing a key by name. */
export function keyRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ hasText: name })
}

/**
 * Create a key through the console UI (Keys page -> "Create key" sheet) and
 * return the plaintext secret shown in the one-time dialog. Leaves the page on
 * /keys with the secret dialog dismissed.
 */
export async function createKeyViaUi(page: Page, name: string): Promise<string> {
  // The header action is always present; the empty state adds a second button
  // of the same name, hence .first().
  await page
    .getByRole('button', { name: 'Create key' })
    .first()
    .click()
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(name)
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  const issued = page.getByTestId('issued-key')
  await expect(issued).toBeVisible({ timeout: 15_000 })
  const secret = ((await issued.textContent()) ?? '').trim()
  await page.getByRole('button', { name: 'Done', exact: true }).click()
  await expect(issued).toBeHidden()
  return secret
}

/**
 * Assert a locator's box lies inside a viewport of the given width: left edge
 * on-screen and right edge within it.
 */
export async function expectInsideViewport(
  locator: Locator,
  viewportWidth: number,
  label: string,
): Promise<void> {
  await locator.scrollIntoViewIfNeeded()
  const box = await locator.boundingBox()
  if (box === null) throw new Error(`${label} has no box`)
  expect(box.x, `${label} left edge on-screen`).toBeGreaterThanOrEqual(0)
  expect(
    box.x + box.width,
    `${label} right edge within ${viewportWidth}px`,
  ).toBeLessThanOrEqual(viewportWidth + 0.5)
}
