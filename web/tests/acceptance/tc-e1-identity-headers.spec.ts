import { platformAccount, vendorRequests } from './fixtures/fake';
import {
  chatWith,
  identity,
  rowOf,
  statement,
  tenantConsumeRows,
} from './fixtures/enterprise';
import { cny4For, quotaFor, walletCNYFor } from './fixtures/pricebook';
import { expect, reverseControl, test } from './fixtures/test';

/**
 * TC-E1 — a customer's own gateway tags every call with who and which
 * department; every cent lands on that employee and that department. A key
 * the customer did not mark as its gateway gets the same headers ignored.
 *
 * Would have caught: identity headers honoured from any key (anyone could
 * bill their spend to a colleague), a department header that is silently
 * dropped, an employee row that is rounded differently from the wallet.
 */

const MODEL = 'deepseek-chat';

test('TC-E1 trusted gateway key attributes per department and employee, to the cent', async ({
  acmeAdmin,
  request,
  world,
}) => {
  const ent = world.ent;
  const perCall = quotaFor(MODEL);
  const perCallCny4 = cny4For(perCall);
  const perCallWallet = walletCNYFor(perCall);
  expect(perCall).toBe(450);
  expect(perCallCny4).toBe(66);

  const alice = `alice-${world.runId}@acme.test`;
  const bob = `bob-${world.runId}@acme.test`;
  const carol = `carol-${world.runId}@acme.test`;
  const mallory = `mallory-${world.runId}@acme.test`;

  const projectsBefore = await statement(acmeAdmin, ent, 'project');
  const walletBefore = (await platformAccount(ent.admin.platformAccountId))
    .charged;
  const vendorBefore = (await vendorRequests()).length;

  await test.step('the gateway sends four calls, each with its own identity', async () => {
    // alice x2 in department A by code; bob x1 in department B by name; carol
    // names a department that does not exist.
    const calls: [string, string][] = [
      [alice, ent.depts.a.code],
      [alice, ent.depts.a.code],
      [bob, ent.depts.b.name],
      [carol, `no-such-dept-${world.runId}`],
    ];
    for (const [who, dept] of calls) {
      const res = await chatWith(
        request,
        ent.keys.trusted.key,
        MODEL,
        identity(who, dept),
      );
      expect(res.status(), await res.text()).toBe(200);
    }
  });

  await test.step('every row carries its employee and department', async () => {
    const rows = await tenantConsumeRows(acmeAdmin, ent, ent.keys.trusted.name);
    const mine = (e: string) => rows.filter((r) => r.employee_ref === e);
    expect(mine(alice)).toHaveLength(2);
    expect(mine(bob)).toHaveLength(1);
    expect(mine(carol)).toHaveLength(1);
    for (const r of mine(alice)) {
      expect(r.project_id).toBe(String(ent.depts.a.id));
      expect(r.quota).toBe(String(perCall));
      expect(r.charged_cny).toBe(perCallWallet.toFixed(4));
    }
    expect(mine(bob)[0].project_id).toBe(String(ent.depts.b.id));
    // An unknown department is not invented: the row keeps the key's own
    // project (none), it is not dropped and no project is created.
    expect(mine(carol)[0].project_id).toBe('0');
  });

  await test.step('the statement says the same, per employee and per department', async () => {
    const byEmp = await statement(acmeAdmin, ent, 'employee');
    expect(rowOf(byEmp, alice)).toMatchObject({
      requests: 2,
      quota: 2 * perCall,
      charged_cny4: 2 * perCallCny4,
    });
    expect(rowOf(byEmp, bob)).toMatchObject({
      requests: 1,
      quota: perCall,
      charged_cny4: perCallCny4,
    });
    const after = await statement(acmeAdmin, ent, 'project');
    const grew = (id: number) =>
      (rowOf(after, String(id))?.requests ?? 0) -
      (rowOf(projectsBefore, String(id))?.requests ?? 0);
    expect(grew(ent.depts.a.id)).toBe(2);
    expect(grew(ent.depts.b.id)).toBe(1);
  });

  await reverseControl(
    'the same headers on an ordinary key are ignored, not honoured',
    async () => {
      const res = await chatWith(
        request,
        ent.keys.plain.key,
        MODEL,
        identity(mallory, ent.depts.a.code),
      );
      expect(res.status(), 'ignored headers must not be an error').toBe(200);
      const rows = await tenantConsumeRows(acmeAdmin, ent, ent.keys.plain.name);
      expect(rows.filter((r) => r.employee_ref === mallory)).toHaveLength(0);
      // It is billed to the employee the key was issued to, and not to A.
      const last = rows[rows.length - 1];
      expect(last.employee_ref).toBe(ent.keys.plain.employeeRef);
      expect(last.project_id).not.toBe(String(ent.depts.a.id));
      const byEmp = await statement(acmeAdmin, ent, 'employee');
      expect(rowOf(byEmp, mallory)).toBeUndefined();
    },
  );

  await test.step('five calls reached the vendor, and the wallet paid for exactly five', async () => {
    const vendor = (await vendorRequests()).slice(vendorBefore);
    expect(vendor.filter((v) => v.status === 200)).toHaveLength(5);
    const walletAfter = (await platformAccount(ent.admin.platformAccountId))
      .charged;
    expect(Math.round((walletAfter - walletBefore) * 1e4)).toBe(
      5 * perCallCny4,
    );
  });
});
