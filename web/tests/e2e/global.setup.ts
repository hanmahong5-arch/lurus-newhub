import { test as setup, expect } from '@playwright/test';
import { BRIDGE_TOKEN, TENANT_SLUG, E2E_USER_ID } from './helpers/auth';
import { STORAGE_STATE } from '../../playwright.config';

/**
 * One-time bridge login → persisted storageState (cookie + localStorage).
 * Every spec depends on this, so we hit the rate-limited bridge ONCE per run
 * instead of once per test.
 */
setup('authenticate via bridge', async ({ page }) => {
  setup.skip(
    !BRIDGE_TOKEN,
    'E2E_BRIDGE_TOKEN not set — cannot establish a session.',
  );

  const res = await page.request.post(
    `/api/v2/bridge/exchange?token=${encodeURIComponent(BRIDGE_TOKEN)}&user_id=${E2E_USER_ID}`,
  );
  expect(
    res.status(),
    `bridge exchange should return 200 (got ${res.status()}: ${await res.text()})`,
  ).toBe(200);
  const user = (await res.json())?.data ?? {};

  // Seed the SPA's client-side auth state BEFORE the app boots. Using
  // addInitScript (not goto-then-evaluate) avoids a race: a fresh SPA at `/`
  // redirects to /login the instant it sees no `user` in localStorage, which
  // destroys the execution context out from under a post-navigation evaluate.
  // Injecting here guarantees the keys exist before any app script runs. Force
  // the routable slug `lurus` (the bridge reports cosmetic "default").
  await page.addInitScript(
    ([u, slug]) => {
      window.localStorage.setItem('user', JSON.stringify(u));
      window.localStorage.setItem('tenant_slug', slug as string);
    },
    [user, TENANT_SLUG] as const,
  );
  await page.goto('/');
  await page.waitForLoadState('domcontentloaded');

  await page.context().storageState({ path: STORAGE_STATE });
});
