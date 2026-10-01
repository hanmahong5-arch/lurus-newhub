import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, gotoDashboard, v2 } from './helpers/auth';

/**
 * Harness smoke test — proves the local stack + bridge login + tenant routing
 * work end to end. Gateway for the rest of the matrix.
 */
test.describe('smoke — bridge login + token CRUD', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  const tokenName = `e2e-smoke-${Date.now()}`;
  let tokenId = '';

  test('login → dashboard → token create/list/delete (API + UI)', async ({
    page,
  }) => {
    // 1. Shared session (storageState) lands on dashboard (PrivateRoute admits us).
    await gotoDashboard(page);

    // 2. Create a token via API (slug = lurus).
    const createRes = await page.request.post(v2('/tokens'), {
      data: { name: tokenName, remain_quota: 1000 },
    });
    expect(
      createRes.ok(),
      `create token: HTTP ${createRes.status()} ${await createRes.text()}`,
    ).toBeTruthy();
    const created = await createRes.json();
    tokenId = String(created?.data?.id ?? '');
    expect(tokenId).toBeTruthy();

    // 3. List contains it.
    const listRes = await page.request.get(v2('/tokens?p=1&size=100'));
    expect(listRes.ok()).toBeTruthy();
    const listBody = await listRes.json();
    const items =
      listBody?.data?.items ?? listBody?.data?.records ?? listBody?.data ?? [];
    const names = JSON.stringify(items);
    expect(names).toContain(tokenName);

    // 4. UI: token page shows it (name appears in both a title span and a
    //    sub-label, so scope to the exact-text match to satisfy strict mode).
    await page.goto('/console/v2/token');
    await expect(
      page.getByText(tokenName, { exact: true }).first(),
    ).toBeVisible({ timeout: 15_000 });
  });

  test.afterEach(async ({ page }) => {
    if (tokenId) {
      await page.request.delete(v2(`/tokens/${tokenId}`));
    }
  });
});

// Cycle-20 C3. jsdom cannot lay out a grid, so the "detail pane is pushed
// off-screen on a phone" regression is only provable in a real browser.
test.describe('token page — phone viewport', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);
  test.use({ viewport: { width: 390, height: 844 } });

  const tokenName = `e2e-narrow-${Date.now()}`;
  let tokenId = '';

  test('detail pane (snippet, rotate, revoke) stays inside a 390px viewport', async ({
    page,
  }) => {
    const createRes = await page.request.post(v2('/tokens'), {
      data: { name: tokenName, remain_quota: 1000 },
    });
    expect(
      createRes.ok(),
      `create token: HTTP ${createRes.status()} ${await createRes.text()}`,
    ).toBeTruthy();
    tokenId = String((await createRes.json())?.data?.id ?? '');
    expect(tokenId).toBeTruthy();

    await page.goto('/console/v2/token');
    await expect(
      page.getByText(tokenName, { exact: true }).first(),
    ).toBeVisible({ timeout: 15_000 });

    const width = page.viewportSize()!.width;
    // The whole scroll body first: a single over-wide child (it was the
    // English topbar) widens every pane, and the per-element checks below
    // then fail by a fraction of a pixel with no hint of the real cause.
    const overflow = await page
      .locator('.hf-body')
      .evaluate((el) => el.scrollWidth - el.clientWidth);
    expect(overflow, 'page body scrolls sideways').toBeLessThanOrEqual(0);
    const targets = {
      snippet: page.locator('pre').first(),
      rotate: page.getByRole('button', { name: /rotate key|轮换密钥/i }),
      revoke: page.getByRole('button', { name: /^(revoke|撤销)$/i }),
    };
    for (const [label, locator] of Object.entries(targets)) {
      await locator.scrollIntoViewIfNeeded();
      const box = await locator.boundingBox();
      expect(box, `${label} has a box`).not.toBeNull();
      expect(box!.x, `${label} left edge on-screen`).toBeGreaterThanOrEqual(0);
      expect(
        box!.x + box!.width,
        `${label} right edge within ${width}px`,
      ).toBeLessThanOrEqual(width);
    }
  });

  test.afterEach(async ({ page }) => {
    if (tokenId) {
      await page.request.delete(v2(`/tokens/${tokenId}`));
    }
  });
});
