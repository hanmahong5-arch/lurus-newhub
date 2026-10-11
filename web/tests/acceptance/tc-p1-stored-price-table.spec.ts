import {
  charged,
  expectDelta,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import { quotaFor } from './fixtures/pricebook';
import {
  createToken,
  getOption,
  setOption,
  systemOne,
} from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-P1 — a model's list price holds on a deployment whose price table was
 * saved by an operator before the model existed.
 *
 * Production keeps ModelRatio in the options table, and a stored table
 * replaces the code's defaults wholesale. When System One shipped (cycle 21)
 * its default rows therefore never reached production: the first call would
 * have failed "ratio or price not set". The fix prices System One models on
 * a miss; this scenario stores a table without them — production's shape —
 * and requires jev-latest to bill its list price, then shows an operator's
 * own row still wins.
 */

const MODEL = 'jev-latest';
const SYSTEM_ONE = [
  'jev-latest',
  'jev-preview',
  'jev-1.13.0',
  'laya-auto',
  'laya-english',
  'laya-multilingual',
  'laya-typed-decisions',
];

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
  for (const m of SYSTEM_ONE) delete table[m];
  expect(
    Object.keys(table).length,
    'a real table, minus System One',
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

  await test.step('jev-latest bills exactly its list price', async () => {
    const before = await snapshot(scope);
    const res = await systemOne(request, token.key, MODEL);
    expect(res.status(), await res.text()).toBe(200);
    await expectDelta(
      scope,
      before,
      charged([listPrice], { linked: true }),
      'jev-latest',
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
      expect((await systemOne(request, token.key, MODEL)).status()).toBe(200);
      await expectDelta(
        scope,
        before,
        charged([2 * listPrice], { linked: true }),
        'operator row',
      );
    },
  );
});
