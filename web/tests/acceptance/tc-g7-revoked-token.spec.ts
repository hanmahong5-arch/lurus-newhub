import { v2 } from './fixtures/env';
import { vendorRequests } from './fixtures/fake';
import {
  expectDelta,
  NOTHING,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import { chat, createToken, ok } from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-G7 — a disabled or deleted token stops working on the very next
 * request: the same key and body that answered 200 a moment ago answer 401,
 * the vendor is never called, and nothing is charged. No cache grace window.
 */

const MODEL = 'deepseek-chat';

test('TC-G7 disabled and deleted tokens fail at once', async ({
  admin,
  customer,
  request,
  world,
}) => {
  const token = await createToken(customer, {
    name: named(world, 'g7'),
    remain_quota: 1_000_000,
  });
  const scope: LedgerScope = {
    user: customer,
    admin,
    token,
    platformAccountId: world.customer.platformAccountId,
  };

  const refused = async (what: string) => {
    const before = await snapshot(scope);
    const vendorBefore = (await vendorRequests()).length;
    const res = await chat(request, token.key, MODEL);
    expect(res.status(), `${what}: ${await res.text()}`).toBe(401);
    await expectDelta(scope, before, NOTHING, `${what} moves nothing`);
    expect((await vendorRequests()).length, `${what} reached the vendor`).toBe(
      vendorBefore,
    );
  };

  await test.step('positive control: the live token works', async () => {
    expect((await chat(request, token.key, MODEL)).status()).toBe(200);
  });

  await reverseControl('disabled: the next request is 401', async () => {
    await ok(
      await customer.put(v2(`/tokens/${token.id}`), { data: { status: 2 } }),
      'disable',
    );
    await refused('disabled token');
  });

  await test.step('re-enabled: works again', async () => {
    await ok(
      await customer.put(v2(`/tokens/${token.id}`), { data: { status: 1 } }),
      'enable',
    );
    expect((await chat(request, token.key, MODEL)).status()).toBe(200);
  });

  await reverseControl('deleted: the next request is 401', async () => {
    await ok(await customer.delete(v2(`/tokens/${token.id}`)), 'delete');
    await refused('deleted token');
  });
});
