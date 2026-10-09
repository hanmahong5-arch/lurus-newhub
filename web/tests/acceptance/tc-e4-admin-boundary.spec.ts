import { logsAll, parseOther, refusal } from './fixtures/enterprise';
import { ENV, v2 } from './fixtures/env';
import { chat, createToken, ok } from './fixtures/provision';
import { expect, named, reverseControl, test } from './fixtures/test';

/**
 * TC-E4 — the customer's administrator runs the customer's company, not ours:
 * the operator's v1 admin routes stay closed, and the full-tenant log never
 * names the channel or vendor that served a call.
 *
 * Would have caught: a tenant admin mapped onto the global admin role (the
 * v1 routes list channels and users of every tenant), channel names and
 * upstream details leaking through `other`.
 */

const MODEL = 'deepseek-chat';

test('TC-E4 customer admin cannot reach operator routes or see the supply chain', async ({
  acmeAdmin,
  admin,
  request,
  world,
}) => {
  const ent = world.ent;

  await test.step('there is a call to look at', async () => {
    const res = await chat(request, ent.keys.plain.key, MODEL);
    expect(res.status(), await res.text()).toBe(200);
  });

  await test.step('the tenant admin is a plain user globally', async () => {
    expect(ent.admin.globalRole).toBe(1);
  });

  await test.step('the tenant admin does manage the tenant', async () => {
    await ok(
      await acmeAdmin.get(`/api/v2/${ent.tenant.slug}/projects`),
      'projects',
    );
    const rows = await logsAll(acmeAdmin, ent.tenant.slug, 'page_size=50');
    expect(rows.length).toBeGreaterThan(0);
  });

  await test.step('the full-tenant log shows no channel, no vendor, no admin_info', async () => {
    const res = await acmeAdmin.get(
      `/api/v2/${ent.tenant.slug}/logs/all?page_size=100`,
    );
    const text = await res.text();
    expect(res.status()).toBe(200);
    const rows = JSON.parse(text).data.logs as any[];
    for (const r of rows) {
      // Vacuous on v2 today (the field is never filled there); kept as a
      // tripwire should the list start back-filling it. The admin_info
      // checks below are the ones with teeth.
      expect(r.channel_name || '', `row ${r.id}`).toBe('');
      expect(
        parseOther(r).admin_info,
        `row ${r.id} admin_info`,
      ).toBeUndefined();
    }
    expect(text).not.toContain('accept-deepseek');
    expect(text).not.toContain('admin_info');
    expect(text).not.toContain('route_attempts');
    expect(text).not.toContain(new URL(ENV.fakeURL).host);
  });

  await reverseControl(
    'the operator reading the same kind of row does see the channel',
    async () => {
      const probe = await createToken(admin, {
        name: named(world, 'e4-op'),
        unlimited_quota: true,
      });
      const res = await chat(request, probe.key, MODEL);
      expect(res.status(), await res.text()).toBe(200);
      const b = await ok(
        await admin.get(
          v2(`/logs/all?token_name=${encodeURIComponent(probe.name)}`),
        ),
        'operator logs/all',
      );
      const row = (b.data.logs as any[])[0];
      // The v2 list never fills channel_name (a read-only column only the v1
      // list back-fills), so the staff-only signal is `other.admin_info`,
      // which every consume row carries and the tenant view strips.
      expect(
        parseOther(row).admin_info,
        'the operator sees admin_info on the row',
      ).toBeTruthy();
    },
  );

  await reverseControl(
    'the operator v1 routes refuse the tenant admin',
    async () => {
      for (const path of [
        '/api/channel/',
        '/api/channel/search?keyword=accept',
        '/api/user/',
        '/api/log/',
        '/api/redemption/',
      ]) {
        await refusal(await acmeAdmin.get(path), `tenant admin on ${path}`);
      }
      // The control: the operator is let in on the same route.
      const chans = await ok(
        await admin.get('/api/channel/'),
        'operator channels',
      );
      expect(JSON.stringify(chans)).toContain('accept-deepseek');
      // Each refused route exists and does honour a session: the operator
      // is let in on the other two as well.
      await ok(await admin.get('/api/user/'), 'operator users');
      await ok(await admin.get('/api/log/'), 'operator logs');
    },
  );

  await reverseControl(
    'the platform tenant API refuses the tenant admin',
    async () => {
      await refusal(
        await acmeAdmin.get('/api/v2/admin/tenants'),
        'tenant admin on /api/v2/admin/tenants',
      );
    },
  );
});
