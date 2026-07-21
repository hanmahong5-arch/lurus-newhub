import { test, expect } from '@playwright/test';
import { BRIDGE_TOKEN, BRIDGE_SKIP, gotoDashboard } from './helpers/auth';

/**
 * Local matrix — platform admin user management + system options + stats.
 *
 * Root-level (non-tenant-scoped) admin surface: /api/v2/admin/users,
 * /api/v2/admin/options, /api/v2/admin/stats. All gated by RootJWTAuth; the
 * bridge session (user id=1, role=100/root) admits every call here.
 *
 * There is exactly one seeded user (root, id=1) in this local stack — admin
 * user-create is deferred (see matrix-relay-and-access.spec.ts note), so a
 * true cross-user RBAC negative test is STAGE/seed-only. The negative paths
 * exercised here (self-demotion lockout guard, invalid role/status, 404 on a
 * nonexistent id) are all real server-side rejections that don't need a
 * second user.
 *
 * Shapes verified against the live local stack.
 */
test.describe('matrix — admin user management + options + stats', () => {
  test.skip(!BRIDGE_TOKEN, BRIDGE_SKIP);

  test('admin users list contains the root seed user', async ({ page }) => {
    const res = await page.request.get(
      '/api/v2/admin/users?page=1&page_size=20',
    );
    expect(res.ok(), `HTTP ${res.status()}`).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body?.data?.users)).toBe(true);
    expect(body?.data?.total).toBeGreaterThanOrEqual(1);
    const root = (body.data.users as any[]).find((u) => u.id === 1);
    expect(root, JSON.stringify(body.data.users)).toBeTruthy();
    expect(root.username).toBe('root');
    expect(root.role).toBe(100);
  });

  test('admin stats: numeric aggregate shape reflects real DB counts', async ({
    page,
  }) => {
    const res = await page.request.get('/api/v2/admin/stats');
    expect(res.ok(), `HTTP ${res.status()}`).toBeTruthy();
    const data = (await res.json())?.data;
    expect(data?.users?.total).toBeGreaterThanOrEqual(1);
    expect(typeof data?.tokens?.total).toBe('number');
    expect(typeof data?.channels?.total).toBe('number');
    expect(data?.tenants?.total).toBeGreaterThanOrEqual(1);
    expect(typeof data?.quota?.total).toBe('number');
    expect(typeof data?.quota?.used).toBe('number');
  });

  test('update quota + group round-trip (self), restored afterward', async ({
    page,
  }) => {
    const before = await page.request.get(
      '/api/v2/admin/users?page=1&page_size=20',
    );
    const beforeUser = ((await before.json())?.data?.users as any[]).find(
      (u) => u.id === 1,
    );
    expect(beforeUser).toBeTruthy();
    const originalQuota = beforeUser.quota;
    const originalGroup = beforeUser.group;

    const newQuota = originalQuota + 12345;
    const put = await page.request.put('/api/v2/admin/users/1', {
      data: { quota: newQuota, group: 'vip' },
    });
    expect(
      put.ok(),
      `PUT: HTTP ${put.status()} ${await put.text()}`,
    ).toBeTruthy();
    const updated = (await put.json())?.data;
    expect(updated?.quota).toBe(newQuota);
    expect(updated?.group).toBe('vip');

    const restore = await page.request.put('/api/v2/admin/users/1', {
      data: { quota: originalQuota, group: originalGroup },
    });
    expect(restore.ok()).toBeTruthy();
    const restored = (await restore.json())?.data;
    expect(restored?.quota).toBe(originalQuota);
    expect(restored?.group).toBe(originalGroup);
  });

  test('self-demotion is rejected with 403; role stays unchanged', async ({
    page,
  }) => {
    const demote = await page.request.put('/api/v2/admin/users/1', {
      data: { role: 1 },
    });
    expect(demote.status()).toBe(403);
    const body = await demote.json();
    expect(body?.success).toBe(false);
    expect(body?.message).toContain('不能降低自己的权限等级');

    const check = await page.request.get(
      '/api/v2/admin/users?page=1&page_size=20',
    );
    const root = ((await check.json())?.data?.users as any[]).find(
      (u) => u.id === 1,
    );
    expect(root?.role).toBe(100);
  });

  test('invalid role / invalid status are rejected with 400', async ({
    page,
  }) => {
    // 7 is not in the valid-role set {0,1,5,10,100}.
    const badRole = await page.request.put('/api/v2/admin/users/1', {
      data: { role: 7 },
    });
    expect(badRole.status()).toBe(400);
    expect((await badRole.json())?.message).toBe('Invalid role');

    // Only 1 (enabled) / 2 (disabled) are valid statuses.
    const badStatus = await page.request.put('/api/v2/admin/users/1', {
      data: { status: 99 },
    });
    expect(badStatus.status()).toBe(400);
    expect((await badStatus.json())?.message).toBe('Invalid status');
  });

  test('updating a nonexistent user id returns 404', async ({ page }) => {
    const res = await page.request.put('/api/v2/admin/users/999999999', {
      data: { quota: 1 },
    });
    expect(res.status()).toBe(404);
    expect((await res.json())?.message).toBe('User not found');
  });

  test('admin options GET/PUT round-trip a boolean flag, restored afterward', async ({
    page,
  }) => {
    const before = await page.request.get('/api/v2/admin/options');
    expect(before.ok()).toBeTruthy();
    const beforeOptions = (await before.json())?.data as Array<{
      key: string;
      value: string;
    }>;
    const flag = beforeOptions.find((o) => o.key === 'ExposeRatioEnabled');
    expect(flag, JSON.stringify(beforeOptions.slice(0, 5))).toBeTruthy();
    const originalValue = flag!.value; // "true" | "false"
    const flipped = originalValue === 'true' ? false : true;

    const put = await page.request.put('/api/v2/admin/options', {
      data: { key: 'ExposeRatioEnabled', value: flipped },
    });
    expect(
      put.ok(),
      `PUT options: HTTP ${put.status()} ${await put.text()}`,
    ).toBeTruthy();
    expect((await put.json())?.success).toBe(true);

    const after = await page.request.get('/api/v2/admin/options');
    const afterFlag = (
      (await after.json())?.data as Array<{
        key: string;
        value: string;
      }>
    ).find((o) => o.key === 'ExposeRatioEnabled');
    expect(afterFlag?.value).toBe(String(flipped));

    // Restore.
    const restore = await page.request.put('/api/v2/admin/options', {
      data: { key: 'ExposeRatioEnabled', value: originalValue === 'true' },
    });
    expect(restore.ok()).toBeTruthy();
    const restoreCheck = await page.request.get('/api/v2/admin/options');
    const restoredFlag = (
      (await restoreCheck.json())?.data as Array<{
        key: string;
        value: string;
      }>
    ).find((o) => o.key === 'ExposeRatioEnabled');
    expect(restoredFlag?.value).toBe(originalValue);
  });

  test('UI: admin users console page renders the root user row', async ({
    page,
  }) => {
    await gotoDashboard(page);
    await page.goto('/console/v2/admin/users');
    await expect(page.getByTestId('user-row-1')).toBeVisible({
      timeout: 15_000,
    });
  });
});
