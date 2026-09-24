/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import HfLoadError from '../../../components/hifi/HfLoadError';
import { API, showSuccess } from '../../../helpers';
import { classifyLoad, isLoadFailed } from '../../../helpers/loadState';

/*
 * v2 admin — delegated permission grant management (L4, 2026-09-13,
 * auth-security-17/18, console-ux-36). Grant MANAGEMENT is root-only
 * server-side (adminRoute/RootJWTAuth, api-v2-router.go): this page lets
 * root see who holds a grant, issue a new one, and revoke one. What a
 * grant UNLOCKS — the audit-feed GETs under /console/v2/admin/audit — is a
 * separate, narrower gate (middleware.RootOrGranted) this page does not
 * touch.
 *
 * Grants are GLOBAL this cycle: the create form has no tenant selector —
 * the server rejects a non-null tenant_id as GRANT_INVALID.
 */

const fmtWhen = (unixSeconds) => {
  if (!unixSeconds) return '—';
  return new Date(unixSeconds * 1000).toLocaleString();
};

const V2AdminAuthz = () => {
  const { t: tr } = useTranslation();
  const [catalog, setCatalog] = useState(null);
  const [grants, setGrants] = useState([]);
  const [loading, setLoading] = useState(true);
  // How the two reads ended, in classifyLoad()'s vocabulary. Kept as one
  // status rather than a boolean: "nobody holds a delegated grant" and "we
  // could not find out who does" are opposite statements about root-gated
  // powers, and before this the page made the first one for both.
  const [loadStatus, setLoadStatus] = useState(null);
  const forbidden = loadStatus === 'forbidden';
  const loadError = isLoadFailed(loadStatus) && !forbidden;
  const [form, setForm] = useState({
    userId: '',
    resource: '',
    action: '',
    ttlSeconds: '',
  });
  const [submitting, setSubmitting] = useState(false);

  const fetchAll = useCallback(async () => {
    setLoading(true);
    // allSettled, not all: with Promise.all one rejection discards the other
    // read's outcome, so the page could not tell "the catalog 500ed" from
    // "the grant list 500ed".
    const [catalogRes, grantsRes] = await Promise.allSettled([
      API.get('/api/v2/admin/authz/catalog'),
      API.get('/api/v2/admin/authz/grants'),
    ]);
    // classifyLoad reads both shapes (an allSettled entry or a raw axios
    // outcome) and both denial shapes:
    //   - a real 403 / 401, which is what this route answers today — the
    //     Bearer branch returns 403 directly (admin_jwt_auth.go:69-72) and
    //     the session fallback rewrites its v1-shaped refusal into a 403
    //     PERMISSION_DENIED or a 401 (:161-168, rootSessionAuth →
    //     capture.rewriteAsV2Denial);
    //   - a 200 whose body IS a recognisable permission refusal, kept as a
    //     defensive path rather than as a claim about today's server.
    // Anything else that resolves with success:false — a store outage, a
    // maintenance page — is 'error', NOT 'forbidden': announcing "Root
    // access required" for it would be a fresh false statement.
    const outcomes = [classifyLoad(catalogRes), classifyLoad(grantsRes)];
    const failures = outcomes.filter(isLoadFailed);
    const next = failures.includes('forbidden')
      ? 'forbidden'
      : (failures[0] ?? 'ok');
    setLoadStatus(next);
    if (next === 'ok') {
      setCatalog(catalogRes.value.data.data);
      setGrants(grantsRes.value.data.data || []);
    } else {
      // Never leave half a page up: a create form with an unread catalog
      // offers no resources, and a stale list would outlive the failure.
      setCatalog(null);
      setGrants([]);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  const resources = useMemo(() => catalog?.resources || [], [catalog]);
  const actionsForResource = useMemo(() => {
    const entry = resources.find((r) => r.resource === form.resource);
    return entry?.actions || [];
  }, [resources, form.resource]);

  const submitGrant = async (e) => {
    e.preventDefault();
    const userId = parseInt(form.userId, 10);
    if (!userId || userId <= 0 || !form.resource || !form.action) return;
    setSubmitting(true);
    try {
      // ttl_seconds is optional (cycle-9 L1): an empty field means a
      // permanent grant, same as before this field existed — omit the key
      // entirely rather than send null-vs-0 ambiguity to the server. A
      // trimmed-empty field is the only thing that omits the key: a parsed
      // 0 (or a non-numeric value the browser's min='1' should already
      // block) is still SENT, so the server's own 400 GRANT_INVALID is
      // what the user sees — this field must not silently reinterpret 0 as
      // "no ttl" and mint a permanent grant instead (R4/A-6/B-6).
      const rawTtl = form.ttlSeconds.trim();
      const ttlSeconds = rawTtl === '' ? null : parseInt(rawTtl, 10);
      const res = await API.post('/api/v2/admin/authz/grants', {
        user_id: userId,
        resource: form.resource,
        action: form.action,
        tenant_id: null,
        ...(ttlSeconds === null || Number.isNaN(ttlSeconds)
          ? {}
          : { ttl_seconds: ttlSeconds }),
      });
      if (res?.data?.success) {
        showSuccess(tr('console.admin.authz.toast_created', 'Grant created'));
        setForm({ userId: '', resource: '', action: '', ttlSeconds: '' });
        await fetchAll();
      }
    } catch (_) {
      // error toast shown by the API interceptor (409 GRANT_EXISTS /
      // 400 GRANT_INVALID both surface there)
    } finally {
      setSubmitting(false);
    }
  };

  const revokeGrant = async (id) => {
    try {
      const res = await API.delete(`/api/v2/admin/authz/grants/${id}`);
      if (res?.data?.success) {
        showSuccess(tr('console.admin.authz.toast_revoked', 'Grant revoked'));
        await fetchAll();
      }
    } catch (_) {
      // error toast from the interceptor
    }
  };

  return (
    <HFShell
      active='admin-authz'
      crumbs={[
        tr('console.admin.authz.crumb_admin', 'governance'),
        tr('console.admin.authz.crumb', 'permission grants'),
      ]}
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.authz.heading_lbl', 'permission grants')}
          </div>
          <h1 data-testid='authz-headline'>
            {tr('console.admin.authz.heading', 'Delegated admin permissions')}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.authz.sub',
              'grant a narrow root-gated permission to an admin without giving them root · grants are global this cycle',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.authz.forbidden_title',
                'Root access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.authz.forbidden_body',
                'Only root can manage delegated permission grants.',
              )}
            </div>
          </div>
        </div>
      ) : loadError ? (
        // Not "No grants issued yet." — that sentence states who holds
        // delegated root powers, and a read that failed found out nothing.
        <div style={{ padding: 24 }}>
          <HfLoadError
            status={loadStatus}
            title={tr(
              'console.admin.authz.load_error',
              'Couldn’t load delegated grants',
            )}
            onRetry={fetchAll}
            testId='authz-load-error'
            retryTestId='authz-retry'
          />
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          <form
            className='panel'
            style={{ padding: '16px 20px', marginBottom: 16 }}
            onSubmit={submitGrant}
            data-testid='authz-create-form'
          >
            <div className='strong' style={{ marginBottom: 10 }}>
              {tr('console.admin.authz.create_heading', 'New grant')}
            </div>
            <div
              style={{
                display: 'flex',
                gap: 10,
                flexWrap: 'wrap',
                alignItems: 'center',
              }}
            >
              <input
                type='number'
                min='1'
                placeholder={tr('console.admin.authz.field_user_id', 'user id')}
                value={form.userId}
                onChange={(e) =>
                  setForm((f) => ({ ...f, userId: e.target.value }))
                }
                data-testid='authz-input-user-id'
              />
              <select
                value={form.resource}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    resource: e.target.value,
                    action: '',
                  }))
                }
                data-testid='authz-select-resource'
              >
                <option value=''>
                  {tr('console.admin.authz.field_resource', 'resource')}
                </option>
                {resources.map((r) => (
                  <option key={r.resource} value={r.resource}>
                    {r.resource}
                  </option>
                ))}
              </select>
              <select
                value={form.action}
                onChange={(e) =>
                  setForm((f) => ({ ...f, action: e.target.value }))
                }
                disabled={!form.resource}
                data-testid='authz-select-action'
              >
                <option value=''>
                  {tr('console.admin.authz.field_action', 'action')}
                </option>
                {actionsForResource.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </select>
              <input
                type='number'
                min='1'
                max='7776000'
                placeholder={tr(
                  'console.admin.authz.field_ttl',
                  'ttl seconds (optional, max 90d)',
                )}
                value={form.ttlSeconds}
                onChange={(e) =>
                  setForm((f) => ({ ...f, ttlSeconds: e.target.value }))
                }
                data-testid='authz-input-ttl'
              />
              <button
                type='submit'
                className='btn primary'
                disabled={
                  submitting || !form.userId || !form.resource || !form.action
                }
                data-testid='authz-submit'
              >
                {tr('console.admin.authz.submit', 'Grant')}
              </button>
            </div>
          </form>

          <div className='panel'>
            {loading ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : grants.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
                data-testid='authz-empty'
              >
                {tr('console.admin.authz.empty', 'No grants issued yet.')}
              </div>
            ) : (
              <table className='t'>
                <thead>
                  <tr>
                    <th>{tr('console.admin.authz.th_user', 'user id')}</th>
                    <th>{tr('console.admin.authz.th_resource', 'resource')}</th>
                    <th>{tr('console.admin.authz.th_action', 'action')}</th>
                    <th>
                      {tr('console.admin.authz.th_created', 'granted at')}
                    </th>
                    <th>
                      {tr('console.admin.authz.th_expires', 'expires at')}
                    </th>
                    <th>{tr('console.admin.authz.th_status', 'status')}</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {grants.map((g) => {
                    // g.expired (cycle-9 L1) is server-derived from
                    // expires_at vs now — do not recompute it client-side,
                    // the server is the single source of truth for "now".
                    const revoked = !!g.revoked_at;
                    const expired = !!g.expired;
                    const active = !revoked && !expired;
                    return (
                      <tr key={g.id} data-testid={`authz-row-${g.id}`}>
                        <td className='mono'>{g.user_id}</td>
                        <td className='mono muted'>{g.resource}</td>
                        <td className='mono muted'>{g.action}</td>
                        <td className='mono muted'>{fmtWhen(g.created_at)}</td>
                        <td
                          className='mono muted'
                          data-testid={`authz-expires-${g.id}`}
                        >
                          {g.expires_at
                            ? fmtWhen(g.expires_at)
                            : tr('console.admin.authz.no_expiry', 'never')}
                        </td>
                        <td>
                          <span
                            className={active ? 'tag ok' : 'tag'}
                            data-testid={`authz-status-${g.id}`}
                          >
                            {revoked
                              ? tr(
                                  'console.admin.authz.status_revoked',
                                  'revoked',
                                )
                              : expired
                                ? tr(
                                    'console.admin.authz.status_expired',
                                    'expired',
                                  )
                                : tr(
                                    'console.admin.authz.status_active',
                                    'active',
                                  )}
                          </span>
                        </td>
                        <td>
                          {!revoked && (
                            <button
                              type='button'
                              className='btn ghost'
                              onClick={() => revokeGrant(g.id)}
                              data-testid={`authz-revoke-${g.id}`}
                            >
                              {tr('console.admin.authz.revoke', 'Revoke')}
                            </button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </HFShell>
  );
};

export default V2AdminAuthz;
