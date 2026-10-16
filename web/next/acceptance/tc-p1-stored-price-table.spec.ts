import {
  charged,
  expectDelta,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import { HOSTED_FAMILY, HOSTED_MODEL } from './fixtures/models';
import { quotaFor } from './fixtures/pricebook';
import {
  createToken,
  getOption,
  setOption,
  hostedCall,
} from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-P1 — a model's list price holds on a deployment whose price table was
 * saved by an operator before the model existed.
 *
 * Production keeps ModelRatio in the options table, and a stored table
 * replaces the code's defaults wholesale. When the hosted family shipped (cycle 21)
 * its default rows therefore never reached production: the first call would
 * have failed "ratio or price not set". The fix prices the hosted models on
 * a miss; this scenario stores a table without them — production's shape —
 * and requires the hosted model to bill its list price, then shows an operator's
 * own row still wins.
 */

const MODEL = HOSTED_MODEL;

let original: string | undefined;

test.afterEach(async ({ admin }) => {
  if (original !== undefined) await setOption(admin, 'ModelRatio', original);
});

test('TC-P1 a stored price table without the model still bills list price', async ({
  admin,
  customer,
  request,
  world,
}) => {
  const listPrice = quotaFor(MODEL);
  expect(listPrice, 'pricebook: 1000 in at $0.042 per 1M, output free').toBe(
    21,
  );

  original = await getOption(admin, 'ModelRatio');
  expect(original, 'the instance exposes its ModelRatio table').toBeTruthy();
  const table = JSON.parse(original as string) as Record<string, number>;
  for (const m of HOSTED_FAMILY) delete table[m];
  expect(
    Object.keys(table).length,
    'a real table, minus the hosted family',
  ).toBeGreaterThan(50);
  await setOption(admin, 'ModelRatio', JSON.stringify(table));

  const token = await createToken(customer, {
    name: named(world, 'p1'),
    remain_quota: 1_000_000,
  });
  const scope: LedgerScope = {
    user: customer,
    admin,
    token,
    platformAccountId: world.customer.platformAccountId,
  };

  await test.step(`${MODEL} bills exactly its list price`, async () => {
    const before = await snapshot(scope);
    const res = await hostedCall(request, token.key, MODEL);
    expect(res.status(), await res.text()).toBe(200);
    await expectDelta(
      scope,
      before,
      charged([listPrice], { linked: true }),
      MODEL,
    );
  });

  await reverseControl(
    "an operator's own row wins over the list price",
    async () => {
      // Ratio 0.042 = $0.084 per 1M input: double the list price.
      await setOption(
        admin,
        'ModelRatio',
        JSON.stringify({ ...table, [MODEL]: 0.042 }),
      );
      const before = await snapshot(scope);
      expect((await hostedCall(request, token.key, MODEL)).status()).toBe(200);
      await expectDelta(
        scope,
        before,
        charged([2 * listPrice], { linked: true }),
        'operator row',
      );
    },
  );
});
