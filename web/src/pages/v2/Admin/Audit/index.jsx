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
import { API, showError, showSuccess } from '../../../../helpers';

/*
 * v2 admin — audit trail. Wired to the four already-shipped root endpoints:
 *   GET /api/v2/admin/audit/events        list + filters + page/per_page
 *   GET /api/v2/admin/audit/actions       canonical action taxonomy (filter menu)
 *   GET /api/v2/admin/audit/export        CSV download of the current filter
 *   GET /api/v2/admin/audit/chain-verify  recompute the tamper-evidence chain
 *
 * The chain verifier is the reason this page exists as more than a log viewer:
 * audit rows carry a per-tenant SHA-256 hash chain (migration 024), and an
 * auditor's actual question is "can you prove nothing was edited or removed",
 * which no amount of scrolling answers. Breaks are reported with the offending
 * row id so the finding is actionable.
 */

const PER_PAGE = 20;

// Rows written before the chain was enabled (or through the fail-open path)
// carry empty hashes. They are reported, never treated as tampering.
const isLegacyRow = (e) => !e.row_hash;

const fmtTime = (unixSeconds) => {
  if (!unixSeconds) return '—';
  const d = new Date(unixSeconds * 1000);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toISOString().replace('T', ' ').slice(0, 19);
};

const shortHash = (h) => (h ? `${h.slice(0, 10)}…` : '—');

// Details is a JSON string; render it pretty when possible, raw when not.
const formatDetails = (raw) => {
  if (!raw) return '';
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch (_) {
    return raw;
  }
};

const inputStyle = {
  fontFamily: 'var(--hf-mono)',
  fontSize: 12,
  padding: '6px 10px',
  border: '1px solid var(--hf-rule)',
  background: 'var(--hf-sunken)',
  color: 'var(--hf-ink)',
  borderRadius: 2,
  outline: 'none',
};

// ─── Chain verification panel ────────────────────────────────────────────────

const ChainVerifyPanel = ({ result, running, onRun }) => {
  const { t: tr } = useTranslation();

  const broken = result && (result.hash_breaks > 0 || result.link_breaks > 0);
  const clean = result && !broken;

  return (
    <div className='panel' style={{ padding: '16px 20px', marginBottom: 16 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 16,
        }}
      >
        <div>
          <div className='strong'>
            {tr('console.admin.audit.chain_title', 'Tamper-evidence chain')}
          </div>
          <div className='muted' style={{ fontSize: 12, marginTop: 4 }}>
            {tr(
              'console.admin.audit.chain_sub',
              'Recompute each row hash and its link to the previous row. Read-only.',
            )}
          </div>
        </div>
        <button
          type='button'
          className='btn primary'
          data-testid='audit-verify-btn'
          disabled={running}
          onClick={onRun}
        >
          {running
            ? tr('console.admin.audit.verifying', 'verifying…')
            : tr('console.admin.audit.verify', 'verify chain')}
        </button>
      </div>

      {result && (
        <div
          data-testid='audit-verify-result'
          style={{
            marginTop: 14,
            paddingTop: 14,
            borderTop: '1px solid var(--hf-rule)',
            display: 'flex',
            gap: 28,
            flexWrap: 'wrap',
            alignItems: 'flex-start',
          }}
        >
          <div>
            <div className='lbl'>
              {tr('console.admin.audit.chain_verdict', 'verdict')}
            </div>
            <div
              data-testid='audit-verify-verdict'
              className={broken ? 'tag err' : 'tag ok'}
              style={{ marginTop: 4 }}
            >
              {broken
                ? tr('console.admin.audit.chain_broken', 'BREAKS FOUND')
                : tr('console.admin.audit.chain_intact', 'intact')}
            </div>
          </div>
          <div>
            <div className='lbl'>
              {tr('console.admin.audit.chain_checked', 'rows checked')}
            </div>
            <div className='mono' style={{ marginTop: 4 }}>
              {result.checked ?? 0}
            </div>
          </div>
          <div>
            <div className='lbl'>
              {tr('console.admin.audit.chain_hash_breaks', 'hash breaks')}
            </div>
            <div className='mono' style={{ marginTop: 4 }}>
              {result.hash_breaks ?? 0}
            </div>
          </div>
          <div>
            <div className='lbl'>
              {tr('console.admin.audit.chain_link_breaks', 'link breaks')}
            </div>
            <div className='mono' style={{ marginTop: 4 }}>
              {result.link_checks_skipped
                ? tr('console.admin.audit.chain_skipped', 'skipped')
                : (result.link_breaks ?? 0)}
            </div>
          </div>
          <div>
            {/* Legacy rows are expected on any install that predates migration
                024 — surfaced so a non-zero count is not mistaken for a break. */}
            <div className='lbl'>
              {tr('console.admin.audit.chain_legacy', 'legacy (unchained)')}
            </div>
            <div className='mono muted' style={{ marginTop: 4 }}>
              {result.legacy_rows ?? 0}
            </div>
          </div>

          {clean && (
            <div className='muted' style={{ fontSize: 11, maxWidth: 320 }}>
              {tr(
                'console.admin.audit.chain_clean_note',
                'Every chained row in this window re-hashes to its stored value.',
              )}
            </div>
          )}

          {result.first_break && (
            <div
              data-testid='audit-first-break'
              style={{ flexBasis: '100%', fontSize: 11 }}
            >
              <div className='lbl' style={{ color: 'var(--hf-err)' }}>
                {tr(
                  'console.admin.audit.chain_first_break',
                  'first hash break',
                )}
              </div>
              <div className='mono' style={{ marginTop: 4 }}>
                #{result.first_break.id} · expected{' '}
                {shortHash(result.first_break.expected)} · actual{' '}
                {shortHash(result.first_break.actual)}
              </div>
            </div>
          )}
          {result.first_link_break && (
            <div style={{ flexBasis: '100%', fontSize: 11 }}>
              <div className='lbl' style={{ color: 'var(--hf-err)' }}>
                {tr(
                  'console.admin.audit.chain_first_link_break',
                  'first link break',
                )}
              </div>
              <div className='mono' style={{ marginTop: 4 }}>
                #{result.first_link_break.id} · expected{' '}
                {shortHash(result.first_link_break.expected)} · actual{' '}
                {shortHash(result.first_link_break.actual)}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
};

// ─── Page ────────────────────────────────────────────────────────────────────

const V2AdminAudit = () => {
  const { t: tr } = useTranslation();
  const [events, setEvents] = useState([]);
  const [total, setTotal] = useState(0);
  const [actions, setActions] = useState([]);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [page, setPage] = useState(1);
  const [expanded, setExpanded] = useState(null);

  const [filters, setFilters] = useState({
    action: '',
    resource: '',
    actor_id: '',
  });

  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState(null);

  const fetchEvents = useCallback(async (pageNum, f) => {
    setLoading(true);
    try {
      const params = { page: pageNum, per_page: PER_PAGE };
      if (f.action) params.action = f.action;
      if (f.resource) params.resource = f.resource;
      if (f.actor_id) params.actor_id = f.actor_id;

      const res = await API.get('/api/v2/admin/audit/events', { params });
      if (res?.data?.success) {
        setEvents(res.data.data.events ?? []);
        setTotal(res.data.data.total ?? 0);
      }
    } catch (err) {
      if (err?.response?.status === 403) setForbidden(true);
    } finally {
      setLoading(false);
    }
  }, []);

  // Action taxonomy powers the filter menu; a failure here must not blank the
  // page — the free-text filters still work.
  useEffect(() => {
    (async () => {
      try {
        const res = await API.get('/api/v2/admin/audit/actions');
        if (res?.data?.success) setActions(res.data.data.actions ?? []);
      } catch (_) {
        /* filter menu degrades to "all actions" */
      }
    })();
  }, []);

  useEffect(() => {
    fetchEvents(page, filters);
  }, [page, filters, fetchEvents]);

  const runVerify = async () => {
    setVerifying(true);
    try {
      const res = await API.get('/api/v2/admin/audit/chain-verify');
      if (res?.data?.success) {
        const data = res.data.data || {};
        setVerifyResult(data);
        if ((data.hash_breaks ?? 0) > 0 || (data.link_breaks ?? 0) > 0) {
          showError(
            tr(
              'console.admin.audit.toast_breaks',
              'Audit chain verification found breaks',
            ),
          );
        } else {
          showSuccess(
            tr('console.admin.audit.toast_intact', 'Audit chain intact'),
          );
        }
      }
    } catch (err) {
      if (err?.response?.status === 403) setForbidden(true);
    } finally {
      setVerifying(false);
    }
  };

  // Export goes through a plain link rather than API.get: the response is a CSV
  // attachment, and letting the browser handle it keeps the file out of JS memory.
  const exportHref = () => {
    const qs = new URLSearchParams({ format: 'csv' });
    if (filters.action) qs.set('action', filters.action);
    if (filters.resource) qs.set('resource', filters.resource);
    if (filters.actor_id) qs.set('actor_id', filters.actor_id);
    return `/api/v2/admin/audit/export?${qs.toString()}`;
  };

  const setFilter = (key, value) => {
    setPage(1);
    setFilters((f) => ({ ...f, [key]: value }));
  };

  const totalPages = Math.max(1, Math.ceil(total / PER_PAGE));

  return (
    <HFShell
      active='admin-audit'
      crumbs={[
        tr('console.admin.audit.crumb_admin', 'governance'),
        tr('console.admin.audit.crumb', 'audit'),
      ]}
      actions={
        !forbidden && (
          <a
            className='btn ghost'
            data-testid='audit-export-btn'
            href={exportHref()}
            download
          >
            {tr('console.admin.audit.export_csv', '↓ export csv')}
          </a>
        )
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.audit.heading_lbl', 'audit trail')}
          </div>
          <h1>
            {loading
              ? '…'
              : forbidden
                ? tr(
                    'console.admin.audit.forbidden_title',
                    'Admin access required',
                  )
                : tr('console.admin.audit.count', { count: total })}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.audit.sub',
              'who did what · hash-chained · exportable',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.audit.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.audit.forbidden_body',
                'You do not have permission to read the audit trail. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          <ChainVerifyPanel
            result={verifyResult}
            running={verifying}
            onRun={runVerify}
          />

          <div
            style={{
              marginBottom: 16,
              display: 'flex',
              gap: 10,
              flexWrap: 'wrap',
            }}
          >
            <select
              data-testid='audit-action-filter'
              style={{ ...inputStyle, cursor: 'pointer', minWidth: 200 }}
              value={filters.action}
              onChange={(e) => setFilter('action', e.target.value)}
            >
              <option value=''>
                {tr('console.admin.audit.all_actions', 'all actions')}
              </option>
              {actions.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
            <input
              data-testid='audit-resource-filter'
              style={{ ...inputStyle, width: 180 }}
              placeholder={tr('console.admin.audit.ph_resource', 'resource…')}
              value={filters.resource}
              onChange={(e) => setFilter('resource', e.target.value)}
            />
            <input
              data-testid='audit-actor-filter'
              style={{ ...inputStyle, width: 140 }}
              type='number'
              min='0'
              placeholder={tr('console.admin.audit.ph_actor', 'actor id…')}
              value={filters.actor_id}
              onChange={(e) => setFilter('actor_id', e.target.value)}
            />
          </div>

          <div className='panel'>
            {loading ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : events.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {filters.action || filters.resource || filters.actor_id
                  ? tr(
                      'console.admin.audit.empty_filtered',
                      'No audit events match these filters.',
                    )
                  : tr(
                      'console.admin.audit.empty',
                      'No audit events recorded yet.',
                    )}
              </div>
            ) : (
              <table className='t'>
                <thead>
                  <tr>
                    <th>{tr('console.admin.audit.th_time', 'time (utc)')}</th>
                    <th>{tr('console.admin.audit.th_actor', 'actor')}</th>
                    <th>{tr('console.admin.audit.th_action', 'action')}</th>
                    <th>{tr('console.admin.audit.th_resource', 'resource')}</th>
                    <th>{tr('console.admin.audit.th_ip', 'ip')}</th>
                    <th>{tr('console.admin.audit.th_hash', 'row hash')}</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {events.map((e) => (
                    <React.Fragment key={e.id}>
                      <tr data-testid={`audit-row-${e.id}`}>
                        <td className='mono'>{fmtTime(e.timestamp)}</td>
                        <td>
                          <span className='tag'>{e.actor_type || '—'}</span>
                          <span
                            className='faint mono'
                            style={{ marginLeft: 6, fontSize: 10 }}
                          >
                            #{e.actor_id ?? 0}
                          </span>
                        </td>
                        <td className='mono strong'>{e.action}</td>
                        <td className='mono muted'>
                          {e.resource || '—'}
                          {e.resource_id ? ` #${e.resource_id}` : ''}
                        </td>
                        <td className='mono muted'>{e.ip || '—'}</td>
                        <td
                          className='faint mono'
                          data-testid={`audit-hash-${e.id}`}
                          style={{ fontSize: 10 }}
                          title={e.row_hash || ''}
                        >
                          {isLegacyRow(e)
                            ? tr('console.admin.audit.legacy_row', 'legacy')
                            : shortHash(e.row_hash)}
                        </td>
                        <td>
                          <button
                            type='button'
                            className='btn ghost sm'
                            data-testid={`audit-details-btn-${e.id}`}
                            onClick={() =>
                              setExpanded((cur) => (cur === e.id ? null : e.id))
                            }
                          >
                            {expanded === e.id
                              ? tr('console.common.hide', 'hide')
                              : tr('console.admin.audit.details', 'details')}
                          </button>
                        </td>
                      </tr>
                      {expanded === e.id && (
                        <tr data-testid={`audit-details-${e.id}`}>
                          <td
                            colSpan={7}
                            style={{ background: 'var(--hf-sunken)' }}
                          >
                            <pre
                              className='mono'
                              style={{
                                margin: 0,
                                padding: '10px 12px',
                                fontSize: 11,
                                whiteSpace: 'pre-wrap',
                                wordBreak: 'break-all',
                              }}
                            >
                              {formatDetails(e.details) ||
                                tr(
                                  'console.admin.audit.no_details',
                                  '(no details)',
                                )}
                            </pre>
                            <div
                              className='faint mono'
                              style={{ padding: '0 12px 10px', fontSize: 10 }}
                            >
                              request_id: {e.request_id || '—'} · prev_hash:{' '}
                              {shortHash(e.prev_hash)}
                            </div>
                          </td>
                        </tr>
                      )}
                    </React.Fragment>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {!loading && events.length > 0 && (
            <div
              style={{
                marginTop: 12,
                display: 'flex',
                alignItems: 'center',
                gap: 12,
              }}
            >
              <button
                type='button'
                className='btn ghost sm'
                data-testid='audit-prev-page'
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                {tr('console.common.prev', '← prev')}
              </button>
              <span className='muted mono' style={{ fontSize: 11 }}>
                {page} / {totalPages}
              </span>
              <button
                type='button'
                className='btn ghost sm'
                data-testid='audit-next-page'
                disabled={page >= totalPages}
                onClick={() => setPage((p) => p + 1)}
              >
                {tr('console.common.next', 'next →')}
              </button>
            </div>
          )}
        </div>
      )}
    </HFShell>
  );
};

export default V2AdminAudit;
