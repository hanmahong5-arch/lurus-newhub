import { expect, type Page, type APIRequestContext } from '@playwright/test';

/**
 * Local/STAGE e2e auth helper — Layer C bridge login.
 *
 * The bridge endpoint (POST /api/v2/bridge/exchange?token=&user_id=) swaps a
 * static env-gated token for a real gin session cookie. It is registered ONLY
 * when env E2E_BRIDGE_TOKEN is non-empty (prod is safe by construction).
 *
 * Contract (verified against internal/adapter/handler/v2_bridge.go, 2026-06-20):
 *   - METHOD IS POST (not GET) and user_id is REQUIRED.
 *   - Returns 200 + Set-Cookie + JSON {data:{id, role, tenant_slug, ...}}.
 *   - tenant_slug used to come back as "default" — the tenant *id* — for the
 *     seeded default tenant, whose routable slug is `lurus`
 *     (migrations/001+021); /api/v2/default/* is answered 404
 *     TENANT_NOT_FOUND. resolveTenantSlug now reads the row, so the response
 *     carries the routable slug. E2E_TENANT_SLUG still wins here so this
 *     suite pins one known value rather than trusting whatever the instance
 *     under test happens to answer.
 *
 * Why we also seed localStorage: the v2 SPA gates /console/v2/* behind
 * PrivateRoute (checks localStorage `user`) and builds API paths from
 * localStorage `tenant_slug`. page.request.post sets only the cookie, so
 * without seeding the SPA would bounce to login / call /api/v2//... . We
 * inject both BEFORE the app boots via addInitScript.
 */

export const BRIDGE_TOKEN = process.env.E2E_BRIDGE_TOKEN || '';
export const TENANT_SLUG = process.env.E2E_TENANT_SLUG || 'lurus';
export const E2E_USER_ID = process.env.E2E_USER_ID || '1';

/**
 * Relay endpoints (/v1/*, /mj/*) are served by the Go backend. Locally the vite
 * dev server only proxies /api, /mj, /pg — NOT /v1 — so relay calls must target
 * the backend directly. On STAGE the SPA and relay share one origin, so this
 * defaults to E2E_BASE_URL. Locally set E2E_BACKEND_URL=http://localhost:8012.
 */
export const BACKEND_URL =
  process.env.E2E_BACKEND_URL || process.env.E2E_BASE_URL || '';

/** Absolute URL for a backend-served (non-proxied) path, e.g. relay. */
export function backend(path: string): string {
  return `${BACKEND_URL}${path}`;
}

export interface BridgeUser {
  id: number;
  username: string;
  role: number;
  status: number;
  tenant_slug?: string;
  [k: string]: unknown;
}

/**
 * Establish a session via the bridge and seed the SPA's client-side auth state.
 * Returns the bridge user payload. Does NOT navigate — callers decide where to go.
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
  );
  expect(
    res.status(),
    `bridge exchange should return 200 (got ${res.status()}: ${await res.text()})`,
  ).toBe(200);
  const body = await res.json();
  const user = (body?.data ?? {}) as BridgeUser;

  // Seed localStorage before the app boots. Force the routable slug (lurus),
  // not the bridge's cosmetic "default".
  await page.addInitScript(
    ([u, slug]) => {
      window.localStorage.setItem('user', JSON.stringify(u));
      window.localStorage.setItem('tenant_slug', slug as string);
    },
    [user, TENANT_SLUG] as const,
  );

  return user;
}

/**
 * Full login + land on the v2 dashboard. Asserts we stayed on /console/v2/*
 * (i.e. PrivateRoute admitted us — not bounced to a login screen).
 */
export async function loginAndGotoDashboard(
  page: Page,
  userId: string | number = E2E_USER_ID,
): Promise<BridgeUser> {
  const user = await loginViaBridge(page, userId);
  await page.goto('/console/v2/dashboard');
  await expect(page).toHaveURL(/\/console\/v2\/dashboard/, { timeout: 15_000 });
  return user;
}

/**
 * Navigate to the v2 dashboard using the session already present in the
 * context's storageState (no bridge call — avoids the 5/60s rate limit).
 * Asserts PrivateRoute admitted us (we stayed on /console/v2/*).
 */
export async function gotoDashboard(page: Page): Promise<void> {
  await page.goto('/console/v2/dashboard');
  await expect(page).toHaveURL(/\/console\/v2\/dashboard/, { timeout: 15_000 });
}

/**
 * Tenant-scoped v2 API path builder, e.g. v2(`/tokens`) -> /api/v2/lurus/tokens.
 */
export function v2(path: string): string {
  return `/api/v2/${TENANT_SLUG}${path}`;
}

/**
 * Skip-guard message for specs that need the bridge token. Use as:
 *   test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);
 */
export const BRIDGE_SKIP =
  'E2E_BRIDGE_TOKEN not set — skipping bridge e2e. Set it (and run the local ' +
  'stack, or point E2E_BASE_URL at STAGE) to execute this spec.';
