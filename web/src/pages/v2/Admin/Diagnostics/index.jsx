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
import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../../components/hifi/HFShell';
import ConfirmDialog from '../../../../components/common/ConfirmDialog';
import { API, showSuccess } from '../../../../helpers';

/*
 * v2 admin — Diagnostics (L8, cycle 9). One page for two root-gated
 * backends that shipped with no console consumer:
 *   GET    /api/v2/admin/routing/affinity          session-affinity stats
 *   DELETE /api/v2/admin/routing/affinity/:key      purge one binding
 *   DELETE /api/v2/admin/routing/affinity?all=true  purge every binding
 *                                                    this replica can reach
 *   GET    /api/v2/admin/security/totp-stats        TOTP adoption
 * (internal/adapter/handler/v2_admin_routing.go, v2_admin_security.go).
 *
 * This page renders the fields of the two handlers' `data` objects, plus
 * the affinity response's top-level `scope` field (rendered from the
 * response next to the counters, not hard-coded — GetAffinityStatsV2's
 * data literal is "replica" today, v2_admin_routing.go:63, but this page
 * reads whatever value the response carries). There is no "privileged
 * accounts with no second factor" number in the totp-stats response — the
 * security panel says that plainly instead of inventing one.
 *
 * Affinity counters are per-replica in-process state (see
 * GetAffinityStatsV2's doc comment): production runs 3 replicas behind one
 * NodePort, so a reload can land on a different pod and show different
 * numbers. That is the backend's own documented behaviour, not a bug in
 * this page. mem_entries counts the in-process fallback map and reads 0
 * while the live backend is redis (doc/product-integration-guide.md), so
 * this page renders it when backend === 'memory' and shows a caveat label
 * otherwise.
 */

const PURGE_ALL_CONFIRM_TEXT = 'PURGE ALL';

const yesNo = (tr, v) =>
  v ? tr('console.common.yes', 'yes') : tr('console.common.no', 'no');

const fmtPct = (v) => `${Number(v ?? 0).toFixed(1)}%`;

// Classifies one Promise.allSettled result from either GET into a shape
// this page can render without guessing: 'ok' carries the handler's own
// `data` object (plus `scope` when the response has one); 'forbidden'
// covers both refusal shapes documented on fetchAll below; 'error' is a
// genuine backend failure (network error, or the 500 GetAdminTotpStatsV2
// answers when its aggregate query fails, v2_admin_security.go:42-46) and
// must not be rendered as zeroed data.
const classifySettled = (settled) => {
  if (settled.status === 'fulfilled') {
    const body = settled.value?.data;
    if (body?.success) {
      return { status: 'ok', data: body.data, scope: body.scope ?? null };
    }
    return { status: 'forbidden', data: null, scope: null };
  }
  if (settled.reason?.response?.status === 403) {
    return { status: 'forbidden', data: null, scope: null };
  }
  return { status: 'error', data: null, scope: null };
};

const V2AdminDiagnostics = () => {
  const { t: tr } = useTranslation();
  const [affinity, setAffinity] = useState(null);
  const [affinityScope, setAffinityScope] = useState(null);
  const [affStatus, setAffStatus] = useState(null);
  const [totp, setTotp] = useState(null);
  const [totpStatus, setTotpStatus] = useState(null);
  const [loading, setLoading] = useState(true);
  const [purgeKey, setPurgeKey] = useState('');
  const [purgeKeyMsg, setPurgeKeyMsg] = useState('');
  const [purgeAllOpen, setPurgeAllOpen] = useState(false);

  const fetchAll = useCallback(async () => {
    // The two admin routes answer two distinct refusal shapes, and this
    // page must not confuse either one with a genuine backend failure:
    //   - a logged-in non-root session gets HTTP 200 {"success":false,...}
    //     from the session-auth branch
    //     (internal/adapter/middleware/auth.go:305-311)
    //   - a non-root Bearer JWT gets HTTP 403 from RootJWTAuth's JWT
    //     branch (internal/adapter/middleware/admin_jwt_auth.go:92-99)
    // Promise.allSettled (not Promise.all) so one endpoint's genuine
    // failure does not blank the other panel: each GET is classified
    // independently into 'ok' | 'forbidden' | 'error'.
    const [affSettled, totpSettled] = await Promise.allSettled([
      API.get('/api/v2/admin/routing/affinity'),
      API.get('/api/v2/admin/security/totp-stats'),
    ]);

    const affResult = classifySettled(affSettled);
    const totpResult = classifySettled(totpSettled);

    setAffinity(affResult.data);
    setAffinityScope(affResult.scope);
    setAffStatus(affResult.status);
    setTotp(totpResult.data);
    setTotpStatus(totpResult.status);
    setLoading(false);
  }, []);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  const doPurgeKey = async () => {
    const key = purgeKey.trim();
    if (!key) return;
    setPurgeKeyMsg('');
    try {
      const res = await API.delete(
        `/api/v2/admin/routing/affinity/${encodeURIComponent(key)}`,
      );
      if (res?.status === 204) {
        setPurgeKeyMsg(
          tr('console.admin.diagnostics.purge_key_ok', 'binding purged'),
        );
        setPurgeKey('');
        await fetchAll();
      } else {
        // A logged-in non-root session answers this same DELETE with
        // HTTP 200 {"success":false,...} (middleware/auth.go:305-311),
        // which is neither 204 nor a rejected promise — without this
        // branch the button would appear to do nothing.
        setPurgeKeyMsg(
          res?.data?.message ||
            tr('console.admin.diagnostics.purge_key_failed', 'purge failed'),
        );
      }
    } catch (err) {
      if (err?.response?.status === 404) {
        setPurgeKeyMsg(
          tr(
            'console.admin.diagnostics.purge_key_not_found',
            'no such binding',
          ),
        );
      } else {
        setPurgeKeyMsg(
          err?.response?.data?.message ||
            tr('console.admin.diagnostics.purge_key_failed', 'purge failed'),
        );
      }
      // A genuine backend failure (e.g. a 500) is also surfaced by the
      // shared API error-toast interceptor — this handler must not read
      // that failure as "not found" (PurgeAffinityKey's own contract).
    }
  };

  // ConfirmDialog calls onConfirm once its typed-confirmation input
  // matches confirmText === PURGE_ALL_CONFIRM_TEXT (see ConfirmDialog.jsx's
  // `armed` check). The "purge all" button's onClick below wires to
  // setPurgeAllOpen(true), not to this function — this function is passed
  // as ConfirmDialog's onConfirm prop further down.
  const doPurgeAll = async () => {
    try {
      const res = await API.delete('/api/v2/admin/routing/affinity?all=true');
      if (res?.data?.success) {
        const { purged, complete } = res.data.data || {};
        showSuccess(
          complete
            ? tr(
                'console.admin.diagnostics.purge_all_ok',
                'purged {{purged}} binding(s)',
                { purged },
              )
            : tr(
                'console.admin.diagnostics.purge_all_incomplete',
                'purged {{purged}} binding(s) — incomplete, some may remain',
                { purged },
              ),
        );
        setPurgeAllOpen(false);
        await fetchAll();
      }
    } catch (_) {
      // error toast from the interceptor
    }
  };

  const aff = affinity || {};
  const sec = totp || {};
  const forbidden = affStatus === 'forbidden' || totpStatus === 'forbidden';

  return (
    <HFShell
      active='admin-diagnostics'
      crumbs={[
        tr('console.admin.diagnostics.crumb_admin', 'operations & insights'),
        tr('console.admin.diagnostics.crumb', 'diagnostics'),
      ]}
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.diagnostics.heading_lbl', 'diagnostics')}
          </div>
          <h1 data-testid='diag-headline'>
            {loading
              ? '…'
              : forbidden
                ? tr(
                    'console.admin.diagnostics.forbidden_title',
                    'Root access required',
                  )
                : tr(
                    'console.admin.diagnostics.headline',
                    'session affinity + TOTP adoption',
                  )}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.diagnostics.sub',
              'two admin backends with no other console consumer',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.diagnostics.forbidden_title',
                'Root access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.diagnostics.forbidden_body',
                'You do not have permission to view diagnostics. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : loading ? (
        <div className='muted' style={{ padding: 24, fontSize: 12 }}>
          {tr('console.common.loading', 'Loading…')}
        </div>
      ) : (
        <div style={{ padding: 24, display: 'grid', gap: 16 }}>
          {/* Panel A — session affinity */}
          <div className='panel' data-testid='diag-affinity-panel'>
            <div style={{ padding: '14px 16px 0' }} className='strong'>
              {tr(
                'console.admin.diagnostics.affinity_title',
                'session affinity',
              )}
            </div>
            {affStatus === 'error' ? (
              <div
                className='muted'
                style={{ padding: '12px 16px', fontSize: 12 }}
                data-testid='diag-affinity-unavailable'
              >
                {tr(
                  'console.admin.diagnostics.affinity_unavailable',
                  'could not load session-affinity stats — try reloading the page',
                )}
              </div>
            ) : (
              <>
                <div
                  style={{
                    padding: '12px 16px',
                    display: 'grid',
                    gridTemplateColumns: 'repeat(4, minmax(0, 1fr))',
                    gap: 12,
                  }}
                >
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.affinity_backend',
                        'backend',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-affinity-backend'>
                      {aff.backend}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.affinity_enabled',
                        'enabled',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-affinity-enabled'>
                      {yesNo(tr, aff.enabled)}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr('console.admin.diagnostics.affinity_ttl', 'ttl (s)')}
                    </div>
                    <div className='mono' data-testid='diag-affinity-ttl'>
                      {aff.ttl_seconds}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.affinity_mem_entries',
                        'mem entries',
                      )}
                    </div>
                    {aff.backend === 'memory' ? (
                      <div
                        className='mono'
                        data-testid='diag-affinity-mementries'
                      >
                        {aff.mem_entries}
                      </div>
                    ) : (
                      <div
                        className='muted'
                        style={{ fontSize: 11 }}
                        data-testid='diag-affinity-mementries-note'
                      >
                        {tr(
                          'console.admin.diagnostics.affinity_mem_entries_note',
                          'in-process fallback map — reads 0 while the backend is redis',
                        )}
                      </div>
                    )}
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr('console.admin.diagnostics.affinity_hit', 'hit')}
                    </div>
                    <div className='mono' data-testid='diag-affinity-hit'>
                      {aff.hit}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr('console.admin.diagnostics.affinity_miss', 'miss')}
                    </div>
                    <div className='mono' data-testid='diag-affinity-miss'>
                      {aff.miss}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr('console.admin.diagnostics.affinity_stale', 'stale')}
                    </div>
                    <div className='mono' data-testid='diag-affinity-stale'>
                      {aff.stale}
                    </div>
                  </div>
                </div>
                <div
                  className='muted'
                  style={{
                    padding: '0 16px 10px',
                    fontSize: 11,
                    lineHeight: 1.6,
                  }}
                  data-testid='diag-affinity-scope-note'
                >
                  {tr(
                    'console.admin.diagnostics.affinity_scope_note',
                    'scope: {{scope}} — the counters are per-replica; a reload can land on a different replica and show different numbers',
                    { scope: affinityScope ?? '—' },
                  )}
                </div>
              </>
            )}
            <div
              style={{
                padding: '12px 16px',
                borderTop: '1px solid var(--hf-rule)',
                display: 'flex',
                gap: 8,
                alignItems: 'center',
                flexWrap: 'wrap',
              }}
            >
              <input
                type='text'
                value={purgeKey}
                onChange={(e) => setPurgeKey(e.target.value)}
                placeholder={tr(
                  'console.admin.diagnostics.purge_key_placeholder',
                  'affinity key',
                )}
                data-testid='diag-affinity-purge-key-input'
                style={{
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 12,
                  padding: '5px 8px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                  minWidth: 220,
                }}
              />
              <button
                type='button'
                className='btn ghost'
                onClick={doPurgeKey}
                data-testid='diag-affinity-purge-key-btn'
              >
                {tr('console.admin.diagnostics.purge_key_btn', 'purge one')}
              </button>
              <button
                type='button'
                className='btn danger'
                onClick={() => setPurgeAllOpen(true)}
                data-testid='diag-affinity-purge-all-btn'
              >
                {tr('console.admin.diagnostics.purge_all_btn', 'purge all')}
              </button>
              {purgeKeyMsg && (
                <span
                  className='muted'
                  style={{ fontSize: 11 }}
                  data-testid='diag-affinity-purge-key-msg'
                >
                  {purgeKeyMsg}
                </span>
              )}
            </div>
          </div>

          {/* Panel B — security posture (TOTP adoption) */}
          <div className='panel' data-testid='diag-security-panel'>
            <div style={{ padding: '14px 16px 0' }} className='strong'>
              {tr(
                'console.admin.diagnostics.security_title',
                'security posture',
              )}
            </div>
            {totpStatus === 'error' ? (
              <div
                className='muted'
                style={{ padding: '12px 16px 14px', fontSize: 12 }}
                data-testid='diag-totp-unavailable'
              >
                {tr(
                  'console.admin.diagnostics.totp_unavailable',
                  'could not load TOTP adoption stats — try reloading the page',
                )}
              </div>
            ) : (
              <>
                <div
                  style={{
                    padding: '12px 16px',
                    display: 'grid',
                    gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
                    gap: 12,
                  }}
                >
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.totp_enrolled',
                        'enrolled',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-totp-enrolled'>
                      {sec.enrolled}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr('console.admin.diagnostics.totp_pending', 'pending')}
                    </div>
                    <div className='mono' data-testid='diag-totp-pending'>
                      {sec.pending}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.totp_total_users',
                        'total users',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-totp-total'>
                      {sec.total_users}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.totp_adoption_pct',
                        'adoption',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-totp-adoption-pct'>
                      {fmtPct(sec.adoption_pct)}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.totp_exhausted',
                        'backup codes exhausted',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-totp-exhausted'>
                      {sec.backup_codes_exhausted}
                    </div>
                  </div>
                  <div>
                    <div className='muted' style={{ fontSize: 11 }}>
                      {tr(
                        'console.admin.diagnostics.totp_no_codes',
                        'no backup codes issued',
                      )}
                    </div>
                    <div className='mono' data-testid='diag-totp-no-codes'>
                      {sec.no_codes_issued}
                    </div>
                  </div>
                </div>
                <div
                  className='muted'
                  style={{
                    padding: '0 16px 14px',
                    fontSize: 11,
                    lineHeight: 1.6,
                  }}
                  data-testid='diag-totp-no-factor-note'
                >
                  {tr(
                    'console.admin.diagnostics.totp_no_factor_note',
                    'the count of privileged accounts with no second factor is not exposed by this API today',
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      )}

      <ConfirmDialog
        visible={purgeAllOpen}
        title={tr(
          'console.admin.diagnostics.purge_all_confirm_title',
          'Purge every session-affinity binding this replica can reach?',
        )}
        consequenceList={[
          tr(
            'console.admin.diagnostics.purge_all_consequence',
            'Every binding this replica can reach is dropped immediately. This cannot be undone.',
          ),
        ]}
        confirmText={PURGE_ALL_CONFIRM_TEXT}
        confirmButtonText={tr(
          'console.admin.diagnostics.purge_all_confirm_btn',
          'purge all',
        )}
        onConfirm={doPurgeAll}
        onCancel={() => setPurgeAllOpen(false)}
      />
    </HFShell>
  );
};

export default V2AdminDiagnostics;
