import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, gotoDashboard, v2 } from './helpers/auth';

/**
 * Phase E3 — audit taxonomy + export endpoint.
 *
 * REWRITTEN 2026-06-20:
 *   - bridge login POST + user_id; slug = lurus.
 *   - audit endpoints are ROOT-level /api/v2/admin/audit/* (not tenant-scoped).
 *   - real CSV header is `id,tenant_id,timestamp,actor_type,actor_id,action,
 *     resource,resource_id,ip,request_id,retention_until,details`
 *     (the old spec asserted `id,ts,actor,...` — that was drift).
 *
 * Fully local-testable.
 */
test.describe('Phase E3 — audit export round-trip', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  const tokenName = `e2e-audit-export-${Date.now()}`;
  let createdId = '';

  test('action discovery + CSV/JSON export round-trip', async ({ page }) => {
    await gotoDashboard(page);

    // Lower time bound for the export filter below: on a persistent STAGE DB
    // this suite runs nightly, so id-ASC page 1 (no paging token) can accumulate
    // >limit historical token.updated rows and push this run's row off the
    // page. start_time narrows the export to "just before this mutation"
    // instead, keeping the assertion deterministic regardless of history.
    const startTs = Math.floor(Date.now() / 1000) - 1;

    // Action taxonomy.
    const actionsRes = await page.request.get('/api/v2/admin/audit/actions');
    expect(actionsRes.ok()).toBeTruthy();
    const actions = ((await actionsRes.json())?.data?.actions ??
      []) as string[];
    expect(actions.length).toBeGreaterThanOrEqual(50);
    expect(actions).toContain('token.updated');
    expect(actions).toContain('billing.quota_threshold');

    // Generate an auditable event: create + update a token.
    const createRes = await page.request.post(v2('/tokens'), {
      data: { name: tokenName, remain_quota: 100 },
    });
    expect(createRes.ok()).toBeTruthy();
    createdId = String((await createRes.json())?.data?.id ?? '');
    expect(createdId).toBeTruthy();

    const updateRes = await page.request.put(v2(`/tokens/${createdId}`), {
      data: { remain_quota: 500 },
    });
    expect(updateRes.ok()).toBeTruthy();

    await page.waitForTimeout(1_500);

    // CSV export contains the token.updated row.
    const csvRes = await page.request.get(
      `/api/v2/admin/audit/export?format=csv&action=token.updated&limit=20&start_time=${startTs}`,
    );
    expect(csvRes.ok()).toBeTruthy();
    expect(csvRes.headers()['content-type']).toMatch(/text\/csv/);
    const csvBody = await csvRes.text();
    expect(csvBody.split('\n')[0]).toMatch(
      /^id,tenant_id,timestamp,actor_type/,
    );
    expect(csvBody).toContain('token.updated');
    expect(csvBody).toContain(createdId);

    // JSON export is paginated + non-empty.
    const jsonRes = await page.request.get(
      `/api/v2/admin/audit/export?format=json&action=token.updated&limit=20&start_time=${startTs}`,
    );
    expect(jsonRes.ok()).toBeTruthy();
    const jsonBody = await jsonRes.json();
    expect(Array.isArray(jsonBody?.data?.events)).toBe(true);
    expect(jsonBody?.data?.events?.length).toBeGreaterThan(0);

    // Bad format rejected.
    const badRes = await page.request.get(
      '/api/v2/admin/audit/export?format=xml',
    );
    expect(badRes.status()).toBeGreaterThanOrEqual(400);
  });

  test.afterEach(async ({ page }) => {
    if (createdId) {
      await page.request.delete(v2(`/tokens/${createdId}`));
    }
  });
});
