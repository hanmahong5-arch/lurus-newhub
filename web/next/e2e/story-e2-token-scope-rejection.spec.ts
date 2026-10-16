import { test, expect } from '@playwright/test';
import {
  BRIDGE_TOKEN,
  BRIDGE_SKIP,
  gotoDashboard,
  v2,
  backend,
} from './helpers/auth';

/**
 * Phase E2 — per-token relay scope allowlist.
 *
 * REWRITTEN 2026-06-20:
 *   - bridge login is POST + user_id; slug = lurus.
 *   - audit endpoint is ROOT-level /api/v2/admin/audit/* (the old tenant-scoped
 *     /api/v2/:slug/admin/audit/* path 404s on this branch).
 *   - The chat happy-path (200) needs a real channel/provider → STAGE-only; here
 *     we assert the LOCAL-testable core: a chat-scoped token is REJECTED with 403
 *     on /v1/embeddings and the rejection is audited as auth.scope_rejected.
 *
 * Verified shapes (2026-06-20 live local stack):
 *   embeddings 403 body: {error:{message:"令牌未授权 embedding 范围..."}}
 *   audit row: action=auth.scope_rejected, details={"required_scope":"embedding",...}
 */
test.describe('Phase E2 — token scope rejection', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  const tokenName = `e2e-scope-${Date.now()}`;
  let scopedToken: { id: string; key: string } | null = null;

  test('chat-scoped token: embeddings rejected with 403 + scope_rejected audit', async ({
    page,
  }) => {
    await gotoDashboard(page);

    const createRes = await page.request.post(v2('/tokens'), {
      data: { name: tokenName, remain_quota: 1_000_000, scopes: ['chat'] },
    });
    expect(createRes.ok()).toBeTruthy();
    const created = await createRes.json();
    scopedToken = {
      id: String(created?.data?.id ?? ''),
      key: String(created?.data?.key ?? ''),
    };
    expect(scopedToken.id).toBeTruthy();
    expect(scopedToken.key).toBeTruthy();

    // Baseline audit count for auth.scope_rejected.
    const before = await page.request.get(
      '/api/v2/admin/audit/events?action=auth.scope_rejected&limit=1',
    );
    const baseline = (await before.json())?.data?.total ?? 0;

    // chat-scoped token on /v1/embeddings → 403 + scope message (no upstream needed).
    // Relay is backend-direct (vite doesn't proxy /v1).
    const embedRes = await page.request.post(backend('/v1/embeddings'), {
      headers: { Authorization: `Bearer ${scopedToken.key}` },
      data: { model: 'embed-model-a', input: 'foo' },
    });
    expect(embedRes.status()).toBe(403);
    expect(JSON.stringify(await embedRes.json())).toMatch(
      /scope|范围|embedding/i,
    );

    // The rejection is audited.
    await page.waitForTimeout(1_500);
    const after = await page.request.get(
      '/api/v2/admin/audit/events?action=auth.scope_rejected&limit=10',
    );
    expect(after.ok()).toBeTruthy();
    const afterJson = await after.json();
    expect(afterJson?.data?.total ?? 0).toBeGreaterThan(baseline);
    const row = (afterJson?.data?.events ?? []).find((e: any) =>
      String(e?.details ?? '').includes('embedding'),
    );
    expect(
      row,
      'audit row with required_scope=embedding should exist',
    ).toBeTruthy();

    // NOTE: the chat happy-path (POST /v1/chat/completions → 200) requires a real
    // channel + provider key and is asserted on STAGE, not here.
  });

  test.afterEach(async ({ page }) => {
    if (scopedToken?.id) {
      await page.request.delete(v2(`/tokens/${scopedToken.id}`));
    }
  });
});
