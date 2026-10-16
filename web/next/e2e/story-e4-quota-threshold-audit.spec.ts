import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, gotoDashboard } from './helpers/auth';

/**
 * Phase E4 — quota threshold audit events.
 *
 * REWRITTEN 2026-06-20:
 *   - bridge login POST + user_id.
 *   - audit endpoints are ROOT-level /api/v2/admin/audit/*.
 *
 * The taxonomy assertion is local-testable. Actual billing.quota_threshold rows
 * require live LLM traffic crossing a quota rung (STAGE/provider) — if absent the
 * threshold-row assertions soft-skip with a clear reason (never silently pass).
 */
test.describe('Phase E4 — quota threshold audit', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  test('action present in taxonomy + historical rows queryable', async ({
    page,
  }) => {
    await gotoDashboard(page);

    const actionsRes = await page.request.get('/api/v2/admin/audit/actions');
    expect(actionsRes.ok()).toBeTruthy();
    const actions = ((await actionsRes.json())?.data?.actions ??
      []) as string[];
    expect(actions).toContain('billing.quota_threshold');

    const eventsRes = await page.request.get(
      '/api/v2/admin/audit/events?action=billing.quota_threshold&limit=10',
    );
    expect(eventsRes.ok()).toBeTruthy();
    const events = ((await eventsRes.json())?.data?.events ?? []) as any[];

    test.skip(
      events.length === 0,
      'no billing.quota_threshold rows yet — requires live LLM traffic crossing ' +
        'a quota rung (STAGE/provider). Taxonomy presence already asserted above.',
    );

    const sample = events[0];
    expect(sample?.action).toBe('billing.quota_threshold');
    const details = JSON.parse(String(sample?.details ?? '{}'));
    expect(details).toMatchObject({
      threshold_pct: expect.any(Number),
      usage_percent: expect.any(Number),
    });
    expect([50, 80, 95, 100]).toContain(details.threshold_pct);
  });
});
