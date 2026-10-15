import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, v2 } from './helpers/auth';

/**
 * Channel-key reveal — secure-verification step-up gate (PR #33).
 *
 * Contract (wired by #33, applied to this working tree 2026-06-21):
 *   - Reveal moved from GET to POST /api/channel/:id/key, gated by
 *     RootAuth + CriticalRateLimit + SecureVerificationRequired.
 *   - POST /api/channel/:id/key WITHOUT a fresh verification → 403 with
 *     code VERIFICATION_REQUIRED.
 *   - POST /api/verify stamps a 5-min session re-confirmation → 200.
 *   - POST /api/channel/:id/key after verify → 200 + the raw key.
 *   - The old GET form is gone → 404.
 *
 * /api/* is vite-proxied, so relative URLs work. The verification timestamp
 * lives in the (cookie-store) session; Playwright's context persists the
 * updated Set-Cookie across page.request calls automatically.
 */
test.describe('channel-key reveal — secure-verification gate (#33)', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  let channelId = '';

  test('unverified → 403 VERIFICATION_REQUIRED; after POST /api/verify → 200 + key', async ({
    page,
  }) => {
    // Auth via shared storageState (page.request carries the session cookie).

    // Create a channel with a known upstream key.
    const create = await page.request.post(v2('/channels'), {
      data: {
        name: `e2e-keyreveal-${Date.now()}`,
        type: 1,
        key: 'sk-reveal-canary',
        base_url: 'https://example.com',
        models: 'model-a',
      },
    });
    expect(create.ok(), await create.text()).toBeTruthy();
    channelId = String((await create.json())?.data?.id ?? '');
    expect(channelId).toBeTruthy();

    // 1. Reveal WITHOUT verification → 403 VERIFICATION_REQUIRED.
    const blocked = await page.request.post(`/api/channel/${channelId}/key`);
    expect(blocked.status()).toBe(403);
    expect((await blocked.json())?.code).toBe('VERIFICATION_REQUIRED');

    // 2. The old GET form is gone (route is POST-only now).
    const oldGet = await page.request.get(`/api/channel/${channelId}/key`);
    expect(oldGet.status()).toBe(404);

    // 3. Step-up verify → 200.
    const verify = await page.request.post('/api/verify', {
      data: { method: 'session' },
    });
    expect(verify.status()).toBe(200);
    expect((await verify.json())?.data?.verified).toBe(true);

    // status endpoint reflects it
    const status = await page.request.get('/api/verify/status');
    expect((await status.json())?.data?.verified).toBe(true);

    // 4. Reveal AFTER verification → 200 + the raw key.
    const reveal = await page.request.post(`/api/channel/${channelId}/key`);
    expect(
      reveal.status(),
      `reveal after verify: ${reveal.status()} ${await reveal.text()}`,
    ).toBe(200);
    const body = await reveal.json();
    expect(body?.success).toBe(true);
    expect(body?.data?.key).toBe('sk-reveal-canary');
  });

  test.afterEach(async ({ page }) => {
    if (channelId) {
      await page.request.delete(v2(`/channels/${channelId}`));
    }
  });
});
