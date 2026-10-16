import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, v2 } from './helpers/auth';

/**
 * Local matrix — tenant-scoped CRUD + read-only projections.
 * All endpoints reachable with the bridge session cookie (UserAuth/AdminAuth/
 * RootJWTAuth all admit the root session). No external provider needed.
 *
 * Shapes verified against the live local stack 2026-06-20.
 */
test.describe('matrix — CRUD + projections', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);
  // Auth comes from the shared storageState (see global.setup.ts) — page.request
  // carries the session cookie; no per-test bridge login (avoids the 5/60s limit).

  test('channel CRUD', async ({ page }) => {
    const name = `e2e-ch-${Date.now()}`;
    const create = await page.request.post(v2('/channels'), {
      data: {
        name,
        type: 1,
        key: 'sk-e2e-secret',
        base_url: 'https://example.com',
        models: 'model-a',
      },
    });
    expect(create.ok(), await create.text()).toBeTruthy();
    const id = String((await create.json())?.data?.id ?? '');
    expect(id).toBeTruthy();

    const get = await page.request.get(v2(`/channels/${id}`));
    expect(get.ok()).toBeTruthy();
    expect((await get.json())?.data?.name).toBe(name);

    const upd = await page.request.put(v2(`/channels/${id}`), {
      data: {
        id: Number(id),
        name: `${name}-upd`,
        type: 1,
        key: 'sk-e2e-secret',
        models: 'model-a',
      },
    });
    expect(upd.ok()).toBeTruthy();

    const list = await page.request.get(v2('/channels'));
    expect(list.ok()).toBeTruthy();
    expect(JSON.stringify(await list.json())).toContain(`${name}-upd`);

    const del = await page.request.delete(v2(`/channels/${id}`));
    expect(del.ok()).toBeTruthy();
  });

  test('redemption CRUD', async ({ page }) => {
    const name = `e2e-rc-${Date.now()}`;
    const create = await page.request.post(v2('/redemptions'), {
      data: { name, quota: 1000, count: 1 },
    });
    expect(create.ok(), await create.text()).toBeTruthy();
    const body = await create.json();
    const id = String(body?.data?.codes?.[0]?.id ?? '');
    expect(id, JSON.stringify(body)).toBeTruthy();

    const list = await page.request.get(v2('/redemptions'));
    expect(list.ok()).toBeTruthy();
    expect(JSON.stringify(await list.json())).toContain(name);

    const del = await page.request.delete(v2(`/redemptions/${id}`));
    expect(del.ok()).toBeTruthy();
  });

  test('credit-pool create/get/usage lifecycle', async ({ page }) => {
    // The pool is a SINGLETON per tenant, and DELETE *drains* it (zeros balance)
    // rather than removing the row — so this is get-or-create, not delete+create.
    const create = await page.request.post(
      '/api/v2/admin/tenants/default/credit-pool',
      {
        data: {
          max_balance: 1_000_000,
          reset_period: 'monthly',
          alert_threshold_pct: 80,
        },
      },
    );
    const createBody = await create.json();
    const ok = create.ok() || createBody?.error_code === 'POOL_ALREADY_EXISTS';
    expect(
      ok,
      `create credit-pool: ${create.status()} ${JSON.stringify(createBody)}`,
    ).toBeTruthy();

    const get = await page.request.get(
      '/api/v2/admin/tenants/default/credit-pool',
    );
    expect(get.ok()).toBeTruthy();
    const pool = (await get.json())?.data;
    expect(pool?.tenant_id).toBe('default');
    expect(pool?.reset_period).toBe('monthly');

    const usage = await page.request.get(
      '/api/v2/admin/tenants/default/credit-pool/usage',
    );
    expect(usage.ok()).toBeTruthy();
    expect(Array.isArray((await usage.json())?.data?.draws)).toBe(true);

    // NOTE: topup is NOT exercised here — it requires a platform wallet
    // (lurus_account_id); see matrix-relay-and-access.spec.ts for that boundary.
    // DELETE drains (does not remove) the singleton, so we leave it in place.
  });

  test('read-only projections reachable (models/pricing/logs/admin)', async ({
    page,
  }) => {
    const checks: Array<[string, (j: any) => boolean]> = [
      [v2('/models'), (j) => Array.isArray(j?.data?.items)],
      [v2('/pricing'), (j) => Array.isArray(j?.data?.pricing)],
      [v2('/logs?p=1&size=5'), (j) => Array.isArray(j?.data?.logs)],
      ['/api/v2/admin/tenants', (j) => Array.isArray(j?.data?.tenants)],
      ['/api/v2/admin/users', (j) => Array.isArray(j?.data?.users)],
    ];
    for (const [url, shape] of checks) {
      const res = await page.request.get(url);
      expect(res.ok(), `${url}: HTTP ${res.status()}`).toBeTruthy();
      expect(shape(await res.json()), `${url}: unexpected shape`).toBeTruthy();
    }
  });
});
