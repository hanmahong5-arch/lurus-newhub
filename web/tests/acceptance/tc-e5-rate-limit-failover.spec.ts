import type { APIRequestContext } from '@playwright/test';
import { queueFault, vendorRequests } from './fixtures/fake';
import { parseOther } from './fixtures/enterprise';
import { v2 } from './fixtures/env';
import {
  charged,
  expectDelta,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import { chat, createToken, ok } from './fixtures/provision';
import { quotaFor } from './fixtures/pricebook';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-E5 — a vendor answers 429 on one channel: the call is served by the other
 * channel of the same model, only the call that succeeded is billed, and the
 * channel that said 429 is left alone while it cools down.
 *
 * Would have caught: a 429 reaching the customer while a healthy channel sat
 * idle, billing for the failed attempt, and a cooling channel retried by every
 * following request.
 *
 * The operator's own token is used (it is the staff view that names channels
 * and attempts); the pool of channels is shared by every tenant.
 */

const MODEL = 'deepseek-chat';

interface Row {
  id: number;
  type: number;
  channel: number;
  quota: number;
  other?: unknown;
}

async function consume(admin: APIRequestContext, name: string): Promise<Row[]> {
  const b = await ok(
    await admin.get(
      v2(`/logs/all?token_name=${encodeURIComponent(name)}&page_size=100`),
    ),
    'operator logs/all',
  );
  return (b.data.logs as Row[])
    .filter((r) => r.type === 2)
    .sort((x, y) => x.id - y.id);
}

test('TC-E5 429 on one channel fails over, bills one call, and cools the channel', async ({
  admin,
  request,
  world,
}) => {
  const perCall = quotaFor(MODEL);
  const token = await createToken(admin, {
    name: named(world, 'e5'),
    remain_quota: 5_000_000,
  });
  const scope: LedgerScope = { user: admin, admin, token };
  const call = async () => {
    const res = await chat(request, token.key, MODEL);
    expect(res.status(), await res.text()).toBe(200);
  };

  await reverseControl(
    'without a fault, one call is one attempt and one charge',
    async () => {
      for (let i = 0; i < 4; i++) await call();
      const rows = await consume(admin, token.name);
      expect(rows).toHaveLength(4);
      for (const r of rows) {
        expect(r.quota).toBe(perCall);
        expect(parseOther(r).admin_info?.route_attempts).toBeUndefined();
      }
    },
  );

  const ledgerBefore = await snapshot(scope);
  const vendorBefore = (await vendorRequests()).length;
  await queueFault('rate_limit_429', 1);
  await call();

  // The oracle is admin_info.use_channel (every row carries it, one entry per
  // channel tried): the per-attempt route_attempts trail is not persisted for a
  // single failover today, because the success attempt is recorded after the
  // log row is written (known product gap, see the hand-over notes).
  const { cooled, healthy } =
    await test.step('the 429 call succeeded on the other channel', async () => {
      const vendor = (await vendorRequests()).slice(vendorBefore);
      expect(vendor.map((v) => v.status)).toEqual([429, 200]);
      const rows = await consume(admin, token.name);
      expect(rows).toHaveLength(5);
      const last = rows[rows.length - 1];
      const tried = (parseOther(last).admin_info?.use_channel as string[])?.map(
        Number,
      );
      expect(tried, 'two channels were tried for the row').toHaveLength(2);
      expect(tried[0]).not.toBe(tried[1]);
      expect(last.channel).toBe(tried[1]);
      return { cooled: tried[0], healthy: tried[1] };
    });
  expect([world.channels.deepseek, world.ent.secondDeepseekChannel]).toContain(
    cooled,
  );
  expect([world.channels.deepseek, world.ent.secondDeepseekChannel]).toContain(
    healthy,
  );

  await test.step('only the successful attempt was charged, on every ledger', async () => {
    // The failed attempt's pre-charge came back: user, token, pool and log
    // moved by exactly one call.
    await expectDelta(
      scope,
      ledgerBefore,
      charged([perCall], { linked: false }),
      'one charge for the 429 call',
    );
  });

  await test.step('the cooling channel gets no traffic while it cools', async () => {
    const mark = (await vendorRequests()).length;
    for (let i = 0; i < 6; i++) await call();
    const vendor = (await vendorRequests()).slice(mark);
    expect(vendor).toHaveLength(6);
    expect(vendor.every((v) => v.status === 200)).toBe(true);
    const rows = (await consume(admin, token.name)).slice(5);
    expect(rows).toHaveLength(6);
    for (const r of rows) {
      expect(r.channel, `row ${r.id} used the cooling channel`).toBe(healthy);
      expect(parseOther(r).admin_info?.use_channel).toHaveLength(1);
    }
    expect(rows.some((r) => r.channel === cooled)).toBe(false);
  });
});
