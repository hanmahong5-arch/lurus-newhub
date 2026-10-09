import { v2 } from './fixtures/env';
import { body, mintCode, ok, redeem } from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-C3 — a minted code, redeemed: what the code says, what the redeem
 * reply says and what the wallet shows agree to the unit; the code is marked
 * used by this customer; a replay is refused and credits nothing.
 */

const CODE_QUOTA = 500_000;

test('TC-C3 mint, redeem, three-way reconciliation, replay refused', async ({
  admin,
  customer,
  world,
}) => {
  const code = await mintCode(admin, named(world, 'c3'), CODE_QUOTA);

  await reverseControl(
    'a key one character short is refused and credits nothing',
    async () => {
      const q = (await ok(await customer.get(v2('/user/me')), 'me')).data.quota;
      const r = await body(
        await redeem(customer, code.key.slice(0, 31)),
        'short key',
        400,
      );
      expect(r.success).toBe(false);
      expect(
        (await ok(await customer.get(v2('/user/me')), 'me')).data.quota,
      ).toBe(q);
    },
  );

  const q0 = (await ok(await customer.get(v2('/user/me')), 'me')).data.quota;
  const r = await ok(await redeem(customer, code.key), 'redeem');
  const q1 = (await ok(await customer.get(v2('/user/me')), 'me')).data.quota;

  await test.step('code face value = reply = wallet delta', async () => {
    expect(r.data.quota_added).toBe(CODE_QUOTA);
    expect(q1 - q0).toBe(CODE_QUOTA);
    expect(q1 - q0).toBe(r.data.quota_added);
  });

  await test.step("the operator's list shows it used by this customer", async () => {
    const list = await ok(
      await admin.get(v2('/redemptions?p=1&size=100')),
      'list codes',
    );
    const items: any[] = list.data.redemptions ?? list.data.items ?? [];
    const row = items.find((x) => x.id === code.id);
    expect(
      row,
      `code ${code.id} in ${JSON.stringify(items.map((x) => x.id))}`,
    ).toBeTruthy();
    expect(row.status).not.toBe(1);
    expect(row.used_user_id).toBe(world.customer.userId);
    expect(row.redeemed_time).toBeGreaterThan(0);
    expect(row.quota).toBe(CODE_QUOTA);
  });

  await reverseControl(
    'the same code again is refused and credits nothing',
    async () => {
      const again = await body(await redeem(customer, code.key), 'replay', 400);
      expect(again.success).toBe(false);
      expect(
        (await ok(await customer.get(v2('/user/me')), 'me')).data.quota,
      ).toBe(q1);
    },
  );
});
