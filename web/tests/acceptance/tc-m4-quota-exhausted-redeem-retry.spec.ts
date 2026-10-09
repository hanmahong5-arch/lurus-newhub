import { v2 } from './fixtures/env';
import {
  charged,
  expectDelta,
  NOTHING,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import { quotaFor } from './fixtures/pricebook';
import {
  body,
  chat,
  createToken,
  mintCode,
  ok,
  redeem,
} from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-M4 — a token out of quota gets an actionable 402 and is charged
 * nothing; the customer redeems a code (into the wallet, not the token),
 * raises the token's limit, and the retry succeeds at list price; the same
 * code a second time is refused and credits nothing.
 */

const MODEL = 'deepseek-chat';
const CODE_QUOTA = 1_000_000;

test('TC-M4 402, redeem, raise, retry, replay refused', async ({
  admin,
  customer,
  request,
  world,
}) => {
  const token = await createToken(customer, {
    name: named(world, 'm4'),
    remain_quota: 1,
  });
  const scope: LedgerScope = {
    user: customer,
    admin,
    token,
    platformAccountId: world.customer.platformAccountId,
  };

  const expect402 = async (what: string) => {
    const before = await snapshot(scope);
    const b = await body(await chat(request, token.key, MODEL), what, 402);
    expect(b.error.code).toBe('token_quota_exhausted');
    expect(b.error.code).not.toBe('pool_not_configured');
    expect(b.error.metadata).toMatchObject({
      reason: 'token_quota_exhausted',
      token_remain_quota_units: 1,
    });
    await expectDelta(scope, before, NOTHING, `${what} moves nothing`);
  };

  await reverseControl('a token with 1 unit left is refused with 402', () =>
    expect402('exhausted token'),
  );

  const code = await mintCode(admin, named(world, 'm4-code'), CODE_QUOTA);
  const q0 = (await ok(await customer.get(v2('/user/me')), 'me')).data.quota;

  await test.step('the code credits the wallet exactly', async () => {
    const r = await ok(await redeem(customer, code.key), 'redeem');
    expect(r.data.quota_added).toBe(CODE_QUOTA);
    const q1 = (await ok(await customer.get(v2('/user/me')), 'me')).data.quota;
    expect(q1 - q0).toBe(CODE_QUOTA);
  });

  await test.step("the wallet top-up does not lift the token's own limit", () =>
    expect402('exhausted token after redeem'));

  await test.step('raise the limit; the retry is charged list price', async () => {
    await ok(
      await customer.put(v2(`/tokens/${token.id}`), {
        data: { remain_quota: 1_000_000 },
      }),
      'raise token limit',
    );
    const before = await snapshot(scope);
    expect((await chat(request, token.key, MODEL)).status()).toBe(200);
    await expectDelta(
      scope,
      before,
      charged([quotaFor(MODEL)], { linked: true }),
      'retry',
    );
  });

  await reverseControl(
    'the same code again is refused and credits nothing',
    async () => {
      const q = (await ok(await customer.get(v2('/user/me')), 'me')).data.quota;
      const r = await body(
        await redeem(customer, code.key),
        'replayed redeem',
        400,
      );
      expect(r.success).toBe(false);
      const after = (await ok(await customer.get(v2('/user/me')), 'me')).data
        .quota;
      expect(after).toBe(q);
    },
  );
});
