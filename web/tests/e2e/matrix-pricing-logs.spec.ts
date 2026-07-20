import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, gotoDashboard, v2 } from './helpers/auth';

/**
 * Local matrix — log filtering/aggregation + models/pricing read projections.
 *
 * Relay-consume logs (type=2) need real LLM traffic (STAGE/provider), so the
 * filter/aggregate assertions here drive a real, LOCAL-testable management
 * action instead: the channel-key-reveal flow (see
 * matrix-channel-key-reveal.spec.ts) writes two type=4 (management) rows to
 * the `logs` table ("安全验证成功" / "查看渠道密钥信息 (渠道ID: N)"). That gives
 * a genuine before/after delta to assert GetLogsV2 + GetLogStatV2 against,
 * without asserting a fiction about traffic we can't generate locally.
 *
 * The `models` catalog is a separate `models` table populated by the
 * background model-sync ticker (60 min), NOT synchronously by channel
 * create — so channel CRUD does not make a model appear in real time. The
 * negative/validation paths on ListModelsV2/GetPricingV2 are exercised
 * instead (verified directly against internal/adapter/handler/v2_models.go
 * and v2_pricing.go).
 */
test.describe('matrix — log filtering/aggregation + models/pricing projections', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  let channelId = '';

  test('log filtering: a real management action shows up under type=4; unrelated filter is empty', async ({
    page,
  }) => {
    const statBefore = await page.request.get(v2('/logs/stat?type=4'));
    expect(statBefore.ok()).toBeTruthy();
    const baselineTotal = (await statBefore.json())?.data?.total_requests ?? 0;

    // Drive a real type=4-logging action: create a channel, step-up verify,
    // reveal its key (see matrix-channel-key-reveal.spec.ts for the contract).
    const create = await page.request.post(v2('/channels'), {
      data: {
        name: `e2e-logfilter-${Date.now()}`,
        type: 1,
        key: 'sk-e2e-logfilter-canary',
        base_url: 'https://api.openai.com',
        models: 'gpt-3.5-turbo',
      },
    });
    expect(create.ok(), await create.text()).toBeTruthy();
    channelId = String((await create.json())?.data?.id ?? '');
    expect(channelId).toBeTruthy();

    const verify = await page.request.post('/api/verify', {
      data: { method: 'session' },
    });
    expect(verify.status()).toBe(200);

    const reveal = await page.request.post(`/api/channel/${channelId}/key`);
    expect(reveal.status()).toBe(200);

    await page.waitForTimeout(1_000);

    // The new rows are queryable via GetLogsV2 with type=4.
    const list = await page.request.get(v2('/logs?type=4&p=1&size=50'));
    expect(list.ok()).toBeTruthy();
    const items = ((await list.json())?.data?.logs ?? []) as any[];
    expect(items.length).toBeGreaterThan(0);
    for (const item of items) {
      expect(item.type).toBe(4);
    }
    const revealRow = items.find((it) =>
      String(it.content ?? '').includes(`渠道ID: ${channelId}`),
    );
    expect(revealRow, JSON.stringify(items)).toBeTruthy();

    // GetLogStatV2 aggregate reflects the same filter — real delta, not a
    // hardcoded constant (baseline captured above so reruns don't rot).
    const statAfter = await page.request.get(v2('/logs/stat?type=4'));
    expect(statAfter.ok()).toBeTruthy();
    const afterTotal = (await statAfter.json())?.data?.total_requests ?? 0;
    expect(afterTotal).toBeGreaterThanOrEqual(baselineTotal + 2);

    // An unrelated model_name filter matches nothing — list and stat agree.
    const noMatchList = await page.request.get(
      v2('/logs?model_name=zzz-e2e-nonexistent-model&p=1&size=5'),
    );
    expect(noMatchList.ok()).toBeTruthy();
    const noMatchBody = await noMatchList.json();
    expect(noMatchBody?.data?.total).toBe(0);
    expect(noMatchBody?.data?.logs ?? []).toHaveLength(0);

    const noMatchStat = await page.request.get(
      v2('/logs/stat?model_name=zzz-e2e-nonexistent-model'),
    );
    expect((await noMatchStat.json())?.data?.total_requests).toBe(0);
  });

  test('models catalog: pagination validation + unknown-tenant 404', async ({
    page,
  }) => {
    const badLimit = await page.request.get(v2('/models?limit=0'));
    expect(badLimit.status()).toBe(400);
    expect((await badLimit.json())?.error_code).toBe('INVALID_PAGINATION');

    const badOffset = await page.request.get(v2('/models?offset=-1'));
    expect(badOffset.status()).toBe(400);
    expect((await badOffset.json())?.error_code).toBe('INVALID_PAGINATION');

    const clamped = await page.request.get(v2('/models?limit=999'));
    expect(clamped.ok()).toBeTruthy();
    expect((await clamped.json())?.data?.limit).toBe(100);

    const noMatch = await page.request.get(
      v2('/models?keyword=zzz-e2e-nonexistent-model'),
    );
    expect(noMatch.ok()).toBeTruthy();
    const noMatchBody = await noMatch.json();
    expect(noMatchBody?.data?.total).toBe(0);
    expect(noMatchBody?.data?.items).toHaveLength(0);

    const unknownTenantModels = await page.request.get(
      '/api/v2/does-not-exist-tenant-xyz/models',
    );
    expect(unknownTenantModels.status()).toBe(404);
    expect((await unknownTenantModels.json())?.error_code).toBe(
      'TENANT_NOT_FOUND',
    );
  });

  test('pricing: group_ratio reflects real ratio_setting state; unknown-tenant 404', async ({
    page,
  }) => {
    const pricing = await page.request.get(v2('/pricing'));
    expect(pricing.ok()).toBeTruthy();
    const data = (await pricing.json())?.data;
    expect(Array.isArray(data?.pricing)).toBe(true);
    expect(Array.isArray(data?.vendors)).toBe(true);
    // Seeded default tenant always carries a "default" rating group at 1x.
    expect(data?.group_ratio?.default).toBe(1);

    const unknownTenantPricing = await page.request.get(
      '/api/v2/does-not-exist-tenant-xyz/pricing',
    );
    expect(unknownTenantPricing.status()).toBe(404);
    expect((await unknownTenantPricing.json())?.error_code).toBe(
      'TENANT_NOT_FOUND',
    );
  });

  test('UI: log console page renders the stat header on real data', async ({
    page,
  }) => {
    await gotoDashboard(page);
    await page.goto('/console/v2/log');
    await expect(page.getByTestId('log-stat-header')).toBeVisible({
      timeout: 15_000,
    });
  });

  test.afterEach(async ({ page }) => {
    if (channelId) {
      await page.request.delete(v2(`/channels/${channelId}`));
      channelId = '';
    }
  });
});
