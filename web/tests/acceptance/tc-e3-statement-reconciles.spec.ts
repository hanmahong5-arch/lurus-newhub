import { platformAccount } from './fixtures/fake';
import {
  chatWith,
  identity,
  statement,
  sumRows,
  type Statement,
} from './fixtures/enterprise';
import { cny4For, quotaFor } from './fixtures/pricebook';
import { expect, reverseControl, test } from './fixtures/test';

/**
 * TC-E3 — the month's statement is what the customer pays: every grouping adds
 * up to its own totals, all groupings agree, and the totals equal what the
 * platform wallet was debited, to 0.0001 yuan.
 *
 * Would have caught: totals computed apart from rows, a grouping that drops
 * the unassigned bucket, the statement priced at list while the wallet is
 * charged the settled amount.
 */

const MODEL = 'deepseek-chat';

test('TC-E3 monthly statement equals the sum of its rows and the wallet debit', async ({
  acmeAdmin,
  lead,
  request,
  world,
}) => {
  const ent = world.ent;
  const perCall = quotaFor(MODEL);
  const perCallCny4 = cny4For(perCall);

  const before = await statement(acmeAdmin, ent, 'project');
  const walletBefore = (await platformAccount(ent.admin.platformAccountId))
    .charged;

  await test.step('three more calls, across both departments and the unassigned bucket', async () => {
    const sends: Record<string, string>[] = [
      identity(`e3-a-${world.runId}`, ent.depts.a.code),
      identity(`e3-b-${world.runId}`, ent.depts.b.code),
      {},
    ];
    for (const h of sends) {
      const res = await chatWith(request, ent.keys.trusted.key, MODEL, h);
      expect(res.status(), await res.text()).toBe(200);
    }
  });

  const views: Record<string, Statement> = {};
  for (const g of ['project', 'employee', 'token'] as const) {
    views[g] = await statement(acmeAdmin, ent, g);
  }

  await test.step('each grouping adds up to its totals', async () => {
    for (const [g, s] of Object.entries(views)) {
      expect(s.totals, `group_by=${g}`).toMatchObject(sumRows(s.rows));
    }
  });

  await test.step('every grouping reports the same totals', async () => {
    const t = views.project.totals;
    for (const g of ['employee', 'token']) {
      expect(views[g].totals.requests, g).toBe(t.requests);
      expect(views[g].totals.quota, g).toBe(t.quota);
      expect(views[g].totals.charged_cny4, g).toBe(t.charged_cny4);
    }
  });

  await test.step('the three calls moved the totals by exactly three calls', async () => {
    const t = views.project.totals;
    expect(t.requests - before.totals.requests).toBe(3);
    expect(t.quota - before.totals.quota).toBe(3 * perCall);
    expect(t.charged_cny4 - before.totals.charged_cny4).toBe(3 * perCallCny4);
  });

  await test.step('the totals equal the wallet debit, in 0.0001 yuan', async () => {
    const walletAfter = (await platformAccount(ent.admin.platformAccountId))
      .charged;
    expect(Math.round((walletAfter - walletBefore) * 1e4)).toBe(
      3 * perCallCny4,
    );
    expect(Math.round(walletAfter * 1e4)).toBe(
      views.project.totals.charged_cny4,
    );
    // Every row is charged (the payer is linked), so none is merely priced.
    expect(views.project.totals.priced_cny4).toBe(0);
  });

  await reverseControl(
    'a month without traffic reports zero, and the lead sees less than the admin',
    async () => {
      const empty = await statement(acmeAdmin, ent, 'project', '2000-01');
      expect(empty.rows).toEqual([]);
      expect(empty.totals).toMatchObject({
        requests: 0,
        quota: 0,
        charged_cny4: 0,
        priced_cny4: 0,
      });
      const mine = await statement(lead, ent, 'project');
      expect(mine.totals.charged_cny4).toBeGreaterThan(0);
      expect(mine.totals.charged_cny4).toBeLessThan(
        views.project.totals.charged_cny4,
      );
    },
  );
});
