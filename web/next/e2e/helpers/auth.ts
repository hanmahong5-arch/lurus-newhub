import { expect, type Page } from '@playwright/test'

/**
 * Local/UAT e2e auth helper - bridge login.
 *
 * The bridge endpoint (POST /api/v2/bridge/exchange?token=&user_id=) swaps a
 * static env-gated token for a real gin session cookie. It is registered ONLY
 * when env E2E_BRIDGE_TOKEN is non-empty (prod is safe by construction).
 *
 * Contract (verified against internal/adapter/handler/v2_bridge.go):
 *   - METHOD IS POST (not GET) and user_id is REQUIRED.
 *   - Returns 200 + Set-Cookie + JSON {data:{id, role, tenant_slug, ...}}.
 *   - tenant_slug can come back as "default" - the tenant *id* - for the seeded
 *     default tenant, whose routable slug is `lurus` (migrations/001+021);
 *     /api/v2/default/* is answered 404 TENANT_NOT_FOUND. E2E_TENANT_SLUG wins
 *     for the API-level specs so this suite pins one known value rather than
 *     trusting whatever the instance under test answers.
 *
 * The console itself needs nothing but the cookie: it resolves the signed-in
 * user from GET /api/v2/~/user/me (`~` = "this session's own tenant", resolved
 * server-side), so no client-side auth state has to be seeded. The one thing
 * we do pin in the browser is the UI language, because the console defaults to
 * Chinese and the specs assert the English strings.
 */

export const BRIDGE_TOKEN = process.env.E2E_BRIDGE_TOKEN || ''
export const TENANT_SLUG = process.env.E2E_TENANT_SLUG || 'lurus'
export const E2E_USER_ID = process.env.E2E_USER_ID || '1'

/** localStorage key the console stores its language choice under. */
export const CONSOLE_LANGUAGE_KEY = 'newhub-console:language'

/**
 * Relay endpoints (/v1/*, /mj/*) are served by the Go backend. Locally the dev
 * server only proxies the console APIs - NOT /v1 - so relay calls must target
 * the backend directly. On UAT the console and relay share one origin, so this
 * defaults to E2E_BASE_URL. Locally set E2E_BACKEND_URL=http://localhost:8012.
 */
export const BACKEND_URL =
  process.env.E2E_BACKEND_URL || process.env.E2E_BASE_URL || ''

/** Absolute URL for a backend-served (non-proxied) path, e.g. relay. */
export function backend(path: string): string {
  return `${BACKEND_URL}${path}`
}

export interface BridgeUser {
  id: number
  username: string
  role: number
  status: number
  tenant_slug?: string
  [k: string]: unknown
}

/**
 * Pin the console to English for every document this page loads, BEFORE the app
 * boots (addInitScript runs ahead of any app script). Also persisted into
 * storageState by the setup project so every spec inherits it.
 */
export async function pinEnglish(page: Page): Promise<void> {
  await page.addInitScript((key) => {
    window.localStorage.setItem(key, 'en')
  }, CONSOLE_LANGUAGE_KEY)
}

/**
 * Establish a session via the bridge. Returns the bridge user payload. Does NOT
 * navigate - callers decide where to go.
 *
 * Cookie sharing: page.request shares the cookie jar with the page's
 * BrowserContext, so the session set here is sent on subsequent page.goto().
 */
export async function loginViaBridge(
  page: Page,
  userId: string | number = E2E_USER_ID,
): Promise<BridgeUser> {
  const res = await page.request.post(
    `/api/v2/bridge/exchange?token=${encodeURIComponent(BRIDGE_TOKEN)}&user_id=${userId}`,
  )
  expect(
    res.status(),
    `bridge exchange should return 200 (got ${res.status()}: ${await res.text()})`,
  ).toBe(200)
  const body = await res.json()
  const user = (body?.data ?? {}) as BridgeUser

  await pinEnglish(page)
  return user
}

/** Sign-in routes the console bounces an unauthenticated browser to. */
const SIGN_IN_URL = /\/(login|bridge-login|register)(\/|\?|$)/

/**
 * Open the console dashboard with the session already in the context and
 * assert we STAYED there. An unauthenticated browser is sent to
 * /login?redirect=/dashboard by the 401 handler after the first user/me read,
 * so a bare URL check right after goto() could pass before the bounce
 * happens. We therefore wait for content only the authenticated shell renders
 * (the dashboard section tabs), then assert the URL.
 */
export async function gotoDashboard(page: Page): Promise<void> {
  await page.goto('/dashboard')
  await expect(page.getByTestId('dashboard-tab-overview')).toBeVisible({
    timeout: 20_000,
  })
  await expect(page).toHaveURL(/\/dashboard(\/|\?|$)/)
  await expect(page).not.toHaveURL(SIGN_IN_URL)
}

/**
 * Full login + land on the dashboard. Asserts we stayed on /dashboard (the
 * session was admitted - not bounced to /login).
 */
export async function loginAndGotoDashboard(
  page: Page,
  userId: string | number = E2E_USER_ID,
): Promise<BridgeUser> {
  const user = await loginViaBridge(page, userId)
  await gotoDashboard(page)
  return user
}

/**
 * Tenant-scoped v2 API path builder, e.g. v2(`/tokens`) -> /api/v2/lurus/tokens.
 */
export function v2(path: string): string {
  return `/api/v2/${TENANT_SLUG}${path}`
}

/**
 * Skip-guard message for specs that need the bridge token. Use as:
 *   test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);
 */
export const BRIDGE_SKIP =
  'E2E_BRIDGE_TOKEN not set - skipping bridge e2e. Set it (and run the local ' +
  'stack, or point E2E_BASE_URL at UAT) to execute this spec.'
