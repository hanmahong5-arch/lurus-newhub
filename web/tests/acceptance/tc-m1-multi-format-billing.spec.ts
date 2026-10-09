import { request as pwRequest } from '@playwright/test';
import { CUSTOMER_STATE, ENV, v2 } from './fixtures/env';
import { vendorRequests } from './fixtures/fake';
import {
  charged,
  consumeRows,
  expectDelta,
  NOTHING,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import {
  CNY_PER_USD,
  QUOTA_PER_USD,
  cny4For,
  quotaFor,
  quotaForTopupCNY,
} from './fixtures/pricebook';
import { body, chat, createToken, messages, ok } from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-M1 — the flagship chain, for a linked customer: one token, three wires
 * (chat, chat streamed, Anthropic messages), and every ledger reconciled to
 * the unit against the vendor's list price — local quota, token, log rows,
 * and the platform wallet in yuan at its own precision. Then the platform
 * top-up in the other direction, and the console's log page.
 *
 * Would have caught: #212 (output at the input price: 150 per call, not 450),
 * #210 (the wallet charged ¥0.0009 per call instead of ¥0.0066).
 */

const MODEL = 'deepseek-chat';

test('TC-M1 three wires, one price, every ledger exact', async ({
  admin,
  customer,
  request,
  world,
}) => {
  const perCall = quotaFor(MODEL);
  expect(perCall, 'pricebook: 1000 in + 500 out at $0.30/$1.20 per 1M').toBe(
    450,
  );

  await test.step('the instance converts at the promised rates', async () => {
    const s = await body(await request.get('/api/status'), 'status');
    expect(s.data.quota_per_unit).toBe(QUOTA_PER_USD);
    expect(s.data.usd_exchange_rate).toBe(CNY_PER_USD);
  });

  await test.step('setup top-up credited exactly', async () => {
    expect(world.topup.quota).toBe(quotaForTopupCNY(world.topup.cny));
  });

  const token = await createToken(customer, {
    name: named(world, 'm1'),
    remain_quota: 5_000_000,
  });
  const scope: LedgerScope = {
    user: customer,
    admin,
    token,
    platformAccountId: world.customer.platformAccountId,
  };

  await test.step('the token sees the model', async () => {
    const m = await body(
      await request.get('/v1/models', {
        headers: { Authorization: `Bearer ${token.key}` },
      }),
      '/v1/models',
    );
    expect((m.data as { id: string }[]).map((x) => x.id)).toContain(MODEL);
  });

  await reverseControl(
    'a key that does not exist is refused and moves nothing',
    async () => {
      const before = await snapshot(scope);
      const res = await chat(request, `${token.key.slice(0, -4)}XXXX`, MODEL);
      expect(res.status()).toBe(401);
      await expectDelta(scope, before, NOTHING, 'unknown key');
    },
  );

  const before = await snapshot(scope);
  const balanceBefore = await body(
    await request.get('/v1/billing/balance', {
      headers: { Authorization: `Bearer ${token.key}` },
    }),
    'balance before',
  );
  const vendorBefore = (await vendorRequests()).length;

  await test.step('chat, non-streamed', async () => {
    const res = await chat(request, token.key, MODEL);
    const b = await body(res, 'chat');
    expect(b.usage).toMatchObject({
      prompt_tokens: 1000,
      completion_tokens: 500,
    });
  });

  await test.step('chat, streamed', async () => {
    const res = await chat(request, token.key, MODEL, { stream: true });
    expect(res.status()).toBe(200);
    expect(res.headers()['content-type']).toContain('text/event-stream');
    const text = await res.text();
    expect(text).toMatch(/^data: \{/m);
    expect(text.trimEnd().endsWith('data: [DONE]'), text.slice(-300)).toBe(
      true,
    );
  });

  await test.step('Anthropic messages, same key', async () => {
    const b = await body(await messages(request, token.key, MODEL), 'messages');
    expect(b.usage).toMatchObject({ input_tokens: 1000, output_tokens: 500 });
  });

  await test.step('every ledger moved by exactly three list-price calls', async () => {
    await expectDelta(
      scope,
      before,
      charged([perCall, perCall, perCall], { linked: true }),
      'three deepseek-chat calls',
    );
  });

  await test.step('each log row is one call at list price, and its yuan', async () => {
    const rows = await consumeRows(customer, token.name);
    expect(rows).toHaveLength(3);
    for (const r of rows) {
      expect(r).toMatchObject({
        model_name: MODEL,
        quota: perCall,
        prompt_tokens: 1000,
        completion_tokens: 500,
        charged_cny4: cny4For(perCall),
      });
    }
    expect(rows.filter((r) => r.is_stream)).toHaveLength(1);
  });

  await test.step('the vendor was called exactly once per call', async () => {
    const seen = (await vendorRequests())
      .slice(vendorBefore)
      .filter((r) => r.model === MODEL);
    expect(seen.map((r) => [r.wire, r.stream, r.status])).toEqual([
      ['openai_chat', false, 200],
      ['openai_chat', true, 200],
      ['anthropic', false, 200],
    ]);
  });

  await test.step("the token's self-service balance agrees", async () => {
    const after = await body(
      await request.get('/v1/billing/balance', {
        headers: { Authorization: `Bearer ${token.key}` },
      }),
      'balance after',
    );
    expect(
      Math.round((balanceBefore.balance_lb - after.balance_lb) * QUOTA_PER_USD),
    ).toBe(3 * perCall);
  });

  await test.step('a platform top-up of ¥7.30 credits exactly $1 of quota', async () => {
    const internal = await pwRequest.newContext({
      baseURL: ENV.baseURL,
      extraHTTPHeaders: { 'X-API-Key': world.internalKey },
    });
    const order = named(world, 'm1-topup');
    const meBefore = await ok(await customer.get(v2('/user/me')), 'me');
    const t = await ok(
      await internal.post('/internal/balance/topup', {
        data: {
          user_id: world.customer.userId,
          amount_rmb: 7.3,
          order_id: order,
          reason: 'acceptance',
        },
      }),
      'top-up',
    );
    expect(t.data.amount).toBe(quotaForTopupCNY(7.3));
    expect(t.data.amount).toBe(QUOTA_PER_USD);
    const meAfter = await ok(await customer.get(v2('/user/me')), 'me');
    expect(meAfter.data.quota - meBefore.data.quota).toBe(QUOTA_PER_USD);

    await reverseControl(
      'the same order replayed credits nothing',
      async () => {
        const again = await ok(
          await internal.post('/internal/balance/topup', {
            data: {
              user_id: world.customer.userId,
              amount_rmb: 7.3,
              order_id: order,
              reason: 'acceptance',
            },
          }),
          'replayed top-up',
        );
        expect(again.idempotent).toBe(true);
        const meReplay = await ok(await customer.get(v2('/user/me')), 'me');
        expect(meReplay.data.quota).toBe(meAfter.data.quota);
      },
    );
    await internal.dispose();
  });
});

test.describe('TC-M1 console', () => {
  test.use({ storageState: CUSTOMER_STATE });

  test('TC-M1 the log page lists each call with its price', async ({
    page,
    customer,
    world,
  }) => {
    const name = named(world, 'm1');
    const rows = await consumeRows(customer, name);
    expect(rows, 'run after the API half of TC-M1').toHaveLength(3);

    await page.goto(`/console/v2/log?token_name=${encodeURIComponent(name)}`);
    const table = page.getByTestId('trace-table');
    await expect(table).toBeVisible({ timeout: 20_000 });
    const trs = table.locator('tbody tr');
    await expect(trs).toHaveCount(3);
    for (let i = 0; i < 3; i++) {
      const tr = trs.nth(i);
      await expect(tr).toContainText(MODEL);
      await expect(tr).toContainText(name);
      // 450 quota = $0.0009 at 500000 per dollar, the page's 4-digit format.
      await expect(tr.locator('td').last()).toHaveText('$0.0009');
    }
    await trs.first().click();
    await expect(page.getByTestId('log-detail-charged')).toContainText(
      '¥0.0066',
    );

    await reverseControl('another token name lists none of them', async () => {
      await page.goto(
        `/console/v2/log?token_name=${encodeURIComponent(`${name}-none`)}`,
      );
      await expect(page.getByTestId('trace-table')).toHaveCount(0, {
        timeout: 20_000,
      });
      await expect(page.getByText(/No logs found|暂无/)).toBeVisible();
    });
  });
});
