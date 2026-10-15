import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, v2, backend } from './helpers/auth';

/**
 * Local matrix — relay gate/error paths + access boundaries + settings.
 *
 * Relay HAPPY-PATH (200) is STAGE-only (needs a real channel + provider key).
 * Here we assert the LOCAL-testable gate/error paths and the platform boundary.
 */
test.describe('matrix — relay gates + access + settings', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  test('relay rejects missing/invalid token with 401', async ({ page }) => {
    // Relay is backend-direct (vite doesn't proxy /v1).
    const noAuth = await page.request.post(backend('/v1/chat/completions'), {
      data: {
        model: 'model-a',
        messages: [{ role: 'user', content: 'hi' }],
      },
    });
    expect(noAuth.status()).toBe(401);

    const badAuth = await page.request.post(backend('/v1/chat/completions'), {
      headers: { Authorization: 'Bearer sk-definitely-invalid' },
      data: {
        model: 'model-a',
        messages: [{ role: 'user', content: 'hi' }],
      },
    });
    expect(badAuth.status()).toBe(401);
  });

  test('credit-pool topup surfaces the platform-wallet boundary', async ({
    page,
  }) => {
    // Ensure a pool exists (get-or-create; POOL_ALREADY_EXISTS is fine).
    await page.request.post('/api/v2/admin/tenants/default/credit-pool', {
      data: {
        max_balance: 1_000_000,
        reset_period: 'monthly',
        alert_threshold_pct: 80,
      },
    });

    const topup = await page.request.post(
      '/api/v2/admin/tenants/default/credit-pool/topup',
      { data: { amount: 50_000 } },
    );
    // Local root user has no platform wallet (lurus_account_id unset). Topup is a
    // platform-integrated path → it must NOT silently succeed locally. We assert
    // the explicit boundary signal rather than mocking a wallet.
    const body = await topup.json().catch(() => ({}));
    const blocked =
      topup.status() >= 400 ||
      body?.success === false ||
      /wallet|lurus_account_id|platform/i.test(JSON.stringify(body));
    expect(
      blocked,
      `topup should surface a platform-wallet boundary, got ${topup.status()} ${JSON.stringify(body)}`,
    ).toBeTruthy();

    await page.request.delete('/api/v2/admin/tenants/default/credit-pool');
  });

  test('settings: update own profile display name (PUT user/me)', async ({
    page,
  }) => {
    // Bridge-login user is shared across tests/runs — restore the original
    // display_name so this mutation doesn't leak into sibling tests.
    const before = await page.request.get(v2('/user/me'));
    expect(before.ok()).toBeTruthy();
    const originalDisplayName = (await before.json())?.data?.display_name;

    const newName = `E2E Name ${Date.now()}`;
    const put = await page.request.put(v2('/user/me'), {
      data: { display_name: newName },
    });
    expect(
      put.ok(),
      `PUT user/me: HTTP ${put.status()} ${await put.text()}`,
    ).toBeTruthy();

    const me = await page.request.get(v2('/user/me'));
    expect(me.ok()).toBeTruthy();
    expect((await me.json())?.data?.display_name).toBe(newName);

    const restore = await page.request.put(v2('/user/me'), {
      data: { display_name: originalDisplayName },
    });
    expect(
      restore.ok(),
      `restore user/me: HTTP ${restore.status()}`,
    ).toBeTruthy();
  });

  test('access: root session reaches both UserAuth and RootJWTAuth routes', async ({
    page,
  }) => {
    // UserAuth route
    const me = await page.request.get(v2('/user/me'));
    expect(me.status(), 'UserAuth route').toBe(200);
    // RootJWTAuth route (admin) — bridge session has root role
    const admin = await page.request.get('/api/v2/admin/users');
    expect(admin.status(), 'RootJWTAuth admin route').toBe(200);
    // NOTE: a true RBAC negative test (non-root user rejected from admin) needs a
    // second seeded non-root user; admin user-create is deferred in this build, so
    // that case is STAGE/seed-only.
  });
});
