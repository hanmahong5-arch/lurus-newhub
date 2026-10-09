import {
  chatWith,
  exportRows,
  identity,
  refusal,
  rowOf,
  statement,
  sumRows,
  tenantConsumeRows,
} from './fixtures/enterprise';
import { ok } from './fixtures/provision';
import { expect, reverseControl, test } from './fixtures/test';

/**
 * TC-E2 — a department lead sees their own department and nothing else; asking
 * for another department looks exactly like asking for one that does not exist.
 *
 * Would have caught: a lead reading the whole tenant's logs, a 403 that
 * confirms another department exists, a statement whose totals add up rows the
 * lead cannot see.
 */

const MODEL = 'deepseek-chat';

test('TC-E2 department lead is confined to their department', async ({
  acmeAdmin,
  lead,
  request,
  world,
}) => {
  const ent = world.ent;
  const inA = `lead-a-${world.runId}@acme.test`;
  const inB = `lead-b-${world.runId}@acme.test`;

  await test.step('one call in each department', async () => {
    for (const [who, dept] of [
      [inA, ent.depts.a.code],
      [inB, ent.depts.b.code],
    ]) {
      const res = await chatWith(
        request,
        ent.keys.trusted.key,
        MODEL,
        identity(who, dept),
      );
      expect(res.status(), await res.text()).toBe(200);
    }
  });

  await test.step('the lead exports only department A', async () => {
    const rows = await exportRows(
      lead,
      ent,
      'type=2&max_rows=50000&token_name=' +
        encodeURIComponent(ent.keys.trusted.name),
    );
    expect(rows.length).toBeGreaterThan(0);
    for (const r of rows) {
      expect(r.project_id, `row ${r.request_id}`).toBe(String(ent.depts.a.id));
    }
    expect(rows.map((r) => r.employee_ref)).toContain(inA);
    expect(rows.map((r) => r.employee_ref)).not.toContain(inB);
  });

  await test.step('the lead statement counts only department A', async () => {
    const s = await statement(lead, ent, 'project');
    expect(s.rows.map((r) => r.key)).toEqual([String(ent.depts.a.id)]);
    expect(s.totals).toMatchObject(sumRows(s.rows));
    const emp = await statement(lead, ent, 'employee');
    expect(rowOf(emp, inA)?.requests).toBe(1);
    expect(rowOf(emp, inB)).toBeUndefined();
  });

  await test.step('the lead stat header counts only department A', async () => {
    const tn = encodeURIComponent(ent.keys.trusted.name);
    const rows = await exportRows(
      lead,
      ent,
      `type=2&max_rows=50000&token_name=${tn}`,
    );
    const stat = await ok(
      await lead.get(
        `/api/v2/${ent.tenant.slug}/logs/stat?type=2&token_name=${tn}`,
      ),
      'lead logs/stat',
    );
    expect(stat.data.total_requests).toBe(rows.length);
    expect(stat.data.total_quota).toBe(
      rows.reduce((a, r) => a + Number(r.quota), 0),
    );
    // The tenant has a department-B row for this key, which must not count.
    const all = await tenantConsumeRows(acmeAdmin, ent, ent.keys.trusted.name);
    expect(all.length).toBeGreaterThan(rows.length);
  });

  await reverseControl(
    'another department is a 404, indistinguishable from one that does not exist',
    async () => {
      const other = await lead.get(
        `/api/v2/${ent.tenant.slug}/logs?project_id=${ent.depts.b.id}`,
      );
      const nowhere = await lead.get(
        `/api/v2/${ent.tenant.slug}/logs?project_id=987654321`,
      );
      expect(other.status()).toBe(404);
      expect(nowhere.status()).toBe(404);
      expect(await other.text()).toBe(await nowhere.text());
      const exp = await lead.get(
        `/api/v2/${ent.tenant.slug}/logs/export?project_id=${ent.depts.b.id}`,
      );
      expect(exp.status()).toBe(404);
      const stat = await lead.get(
        `/api/v2/${ent.tenant.slug}/logs/stat?project_id=${ent.depts.b.id}`,
      );
      expect(stat.status()).toBe(404);
      // The department the lead does lead answers.
      const own = await lead.get(
        `/api/v2/${ent.tenant.slug}/logs?project_id=${ent.depts.a.id}`,
      );
      expect(own.status()).toBe(200);
    },
  );

  await reverseControl(
    'the tenant-wide views stay closed to the lead; the admin sees both',
    async () => {
      await refusal(
        await lead.get(`/api/v2/${ent.tenant.slug}/logs/all?page_size=5`),
        'lead on /logs/all',
      );
      const tenantExp = await lead.get(
        `/api/v2/${ent.tenant.slug}/logs/export?scope=tenant&type=2`,
      );
      expect(tenantExp.status(), 'lead on export scope=tenant').toBe(403);
      const all = await exportRows(
        acmeAdmin,
        ent,
        'scope=tenant&type=2&max_rows=50000&token_name=' +
          encodeURIComponent(ent.keys.trusted.name),
      );
      const who = all.map((r) => r.employee_ref);
      expect(who).toContain(inA);
      expect(who).toContain(inB);
    },
  );
});
