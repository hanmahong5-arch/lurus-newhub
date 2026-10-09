import { setVendorUsage } from './fixtures/fake';
import {
  charged,
  expectDelta,
  snapshot,
  type LedgerScope,
} from './fixtures/ledger';
import { quotaFor, type Usage } from './fixtures/pricebook';
import { chat, createToken, messages } from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-M2 — the same usage costs the same in every wire, to the unit.
 *
 * The usage is chosen so rounding decides the answer: 1001 in + 3 out of
 * deepseek-chat is 151.95 quota at list price, so Round gives 152 and a
 * truncating path gives 151. Historically the Anthropic path truncated while
 * the chat path rounded (doc/uat TC-M2); a split of one unit between wires
 * is what this catches.
 */

const MODEL = 'deepseek-chat';
const ODD: Usage = { prompt: 1001, completion: 3 };

test.afterEach(async () => {
  await setVendorUsage({ prompt_tokens: 1000, completion_tokens: 500 });
});

test('TC-M2 one usage, one price, in every wire', async ({
  admin,
  customer,
  request,
  world,
}) => {
  const want = quotaFor(MODEL, ODD);
  expect(want, 'pricebook: 151.95 rounds to 152').toBe(152);
  await setVendorUsage({
    prompt_tokens: ODD.prompt,
    completion_tokens: ODD.completion,
  });

  const token = await createToken(customer, {
    name: named(world, 'm2'),
    remain_quota: 3_000_000,
  });
  const scope: LedgerScope = {
    user: customer,
    admin,
    token,
    platformAccountId: world.customer.platformAccountId,
  };

  const wires = {
    chat: () => chat(request, token.key, MODEL),
    'chat streamed': () => chat(request, token.key, MODEL, { stream: true }),
    'anthropic messages': () => messages(request, token.key, MODEL),
  };
  for (const [wire, call] of Object.entries(wires)) {
    await test.step(`${wire}: exactly ${want}`, async () => {
      const before = await snapshot(scope);
      const res = await call();
      expect(res.status(), await res.text()).toBe(200);
      await expectDelta(scope, before, charged([want], { linked: true }), wire);
    });
  }

  await reverseControl(
    'one more output token is a different price, and is seen',
    async () => {
      const plus: Usage = { prompt: 1001, completion: 4 };
      const other = quotaFor(MODEL, plus);
      expect(other).toBe(153);
      await setVendorUsage({
        prompt_tokens: plus.prompt,
        completion_tokens: plus.completion,
      });
      const before = await snapshot(scope);
      expect((await chat(request, token.key, MODEL)).status()).toBe(200);
      await expectDelta(
        scope,
        before,
        charged([other], { linked: true }),
        'one more output token',
      );
    },
  );
});
