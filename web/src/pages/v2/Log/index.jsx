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
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import NotAvailable from '../../../components/hifi/NotAvailable';
import HfSkeletonRows from '../../../components/hifi/HfSkeletonRows';
import { API, showError } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/* Wave 2: Cluster tab wired. Round 2: Live tail wired via cursor-poll. */

/*
 * v2 Log page — wired to GET /api/v2/:tenant_slug/logs (+ /logs/stat header).
 * TTFT is not stored in the log schema → rendered as an honest n/a, never a
 * silent —. Upstream channel is shown when the row carries a channel id/name,
 * otherwise n/a. The outcome tag is derived from the log `type` (error rows are
 * not painted green). Live tail polls /logs?after_id=<cursor> every 3s: the
 * service runs fixed replicas over shared Postgres, so a stateless cursor-poll
 * lands on any pod where an SSE stream would pin to one and break on churn.
 */

const LOG_TYPE_ERROR = 5;

// Outcome derived from the log type — error logs (type 5) must not render as a
// green "200". We do not store the upstream HTTP status, so this reports the
// recorded outcome class, not a fabricated status code. `label` doubles as the
// i18n key suffix (console.log.outcome_<label>).
const outcomeTag = (r) =>
  Number(r?.type) === LOG_TYPE_ERROR
    ? { cls: 'tag error', label: 'error' }
    : { cls: 'tag ok', label: 'ok' };

// Per-attempt routing trace, written by the relay only when a request bounced
// across channels (single-attempt requests carry none). It lives under
// other.admin_info, which the API strips for non-admin callers — so an empty
// list here means "not applicable or not visible to you", never an error.
const parseRouteAttempts = (row) => {
  if (!row?.other) return [];
  try {
    const other =
      typeof row.other === 'string' ? JSON.parse(row.other) : row.other;
    const attempts = other?.admin_info?.route_attempts;
    return Array.isArray(attempts) ? attempts : [];
  } catch (_) {
    return [];
  }
};

const attemptTagClass = (outcome) =>
  outcome === 'success'
    ? 'tag ok'
    : outcome === 'breaker_open'
      ? 'tag'
      : 'tag error';

const fmtTime = (unixSec) => {
  if (!unixSec) return '—';
  const d = new Date(unixSec * 1000);
  return d.toLocaleTimeString('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
  });
};

const fmtTok = (prompt, completion) => {
  const p = prompt ? `${(prompt / 1000).toFixed(1)}k` : '0';
  const c = completion ? `${(completion / 1000).toFixed(1)}k` : '—';
  return `${p}→${c}`;
};

const fmtCost = (quota) => {
  if (!quota) return '—';
  const usd = quota / getQuotaPerUSD();
  return `$${usd.toFixed(4)}`;
};

const PAGE_SIZE = 50;

// Live-tail tuning. 3s poll matches the plan; the buffer is bounded so a
// long-running tail can't grow memory without limit (drop oldest at the cap).
const LIVE_POLL_MS = 3000;
const LIVE_CAP = 200;

const HFLog = () => {
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` to match the v2 console convention (avoids shadowing by
  // local loop variables named `t`).
  const { t: tr } = useTranslation();

  const [tab, setTab] = useState('trace');
  const [selRow, setSelRow] = useState(0);

  // Live tail (cursor-poll) state. `liveCursor` is a ref (not state) so the
  // interval callback always reads the latest id without re-subscribing.
  const [liveRows, setLiveRows] = useState([]);
  const [liveOn, setLiveOn] = useState(true);
  const liveCursorRef = useRef(0);
  const liveSeededRef = useRef(false);

  // Cluster tab state
  const [clusterBucket, setClusterBucket] = useState('hour');
  const [clusterItems, setClusterItems] = useState([]);
  const [clusterLoading, setClusterLoading] = useState(false);

  // Logs state
  const [logs, setLogs] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);

  // Aggregate stat header (RPM/TPM/total requests/total quota) — wired to
  // GET /logs/stat over the same filters as the trace list.
  const [stat, setStat] = useState(null);
  const [statLoading, setStatLoading] = useState(false);

  // Filter state
  const [filterModel, setFilterModel] = useState('');
  const [filterToken, setFilterToken] = useState('');
  const [filterStart, setFilterStart] = useState('');
  const [filterEnd, setFilterEnd] = useState('');

  const fetchLogs = useCallback(
    async (currentPage, model, token, start, end) => {
      setLoading(true);
      try {
        const params = new URLSearchParams({
          page: String(currentPage),
          page_size: String(PAGE_SIZE),
        });
        if (model) params.set('model_name', model);
        if (token) params.set('token_name', token);
        if (start)
          params.set(
            'start_time',
            String(Math.floor(new Date(start).getTime() / 1000)),
          );
        if (end)
          params.set(
            'end_time',
            String(Math.floor(new Date(end).getTime() / 1000)),
          );

        const res = await API.get(
          `/api/v2/${tenantSlug}/logs?${params.toString()}`,
        );
        if (res?.data?.success) {
          const d = res.data.data;
          setLogs(d.logs ?? []);
          setTotal(d.total ?? 0);
          setSelRow(0);
        } else {
          showError(
            res?.data?.message ||
              tr('console.log.load_failed', 'Failed to load logs'),
          );
        }
      } catch (_) {
        // error toast shown by API interceptor
      } finally {
        setLoading(false);
      }
    },
    [tenantSlug, tr],
  );

  const fetchStat = useCallback(
    async (model, token, start, end) => {
      setStatLoading(true);
      try {
        const params = new URLSearchParams();
        if (model) params.set('model_name', model);
        if (token) params.set('token_name', token);
        if (start)
          params.set(
            'start_time',
            String(Math.floor(new Date(start).getTime() / 1000)),
          );
        if (end)
          params.set(
            'end_time',
            String(Math.floor(new Date(end).getTime() / 1000)),
          );
        const qs = params.toString();
        const res = await API.get(
          `/api/v2/${tenantSlug}/logs/stat` + (qs ? `?${qs}` : ''),
        );
        if (res?.data?.success) setStat(res.data.data);
      } catch (_) {
        // non-fatal: the header simply shows — until the next successful fetch
      } finally {
        setStatLoading(false);
      }
    },
    [tenantSlug],
  );

  // Fetch on mount and whenever tenantSlug changes
  useEffect(() => {
    if (tenantSlug) {
      fetchLogs(page, filterModel, filterToken, filterStart, filterEnd);
      fetchStat(filterModel, filterToken, filterStart, filterEnd);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tenantSlug]);

  const fetchCluster = useCallback(
    async (bucket) => {
      setClusterLoading(true);
      try {
        const res = await API.get(
          `/api/v2/${tenantSlug}/logs/cluster?bucket=${bucket}`,
        );
        if (res?.data?.success) {
          const items = res.data.data.items ?? [];
          // Sort descending by count (API returns sorted, but guard client-side)
          items.sort((a, b) => b.count - a.count);
          setClusterItems(items);
        } else {
          showError(
            res?.data?.message ||
              tr(
                'console.log.cluster_load_failed',
                'Failed to load cluster data',
              ),
          );
        }
      } catch (_) {
        // error toast shown by API interceptor
      } finally {
        setClusterLoading(false);
      }
    },
    [tenantSlug, tr],
  );

  // Fetch cluster when switching to cluster tab or bucket changes
  useEffect(() => {
    if (tab === 'cluster' && tenantSlug) {
      fetchCluster(clusterBucket);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, clusterBucket, tenantSlug]);

  // ── Live tail (cursor-poll) ───────────────────────────────────────────────
  // disableDuplicate bypasses api.js's in-flight-GET dedup so a steady poll
  // loop is never coalesced into a single shared promise.
  const fetchLivePage = useCallback(
    async (afterId) => {
      const params = new URLSearchParams({ page_size: '50' });
      if (afterId > 0) params.set('after_id', String(afterId));
      const res = await API.get(
        `/api/v2/${tenantSlug}/logs?${params.toString()}`,
        { disableDuplicate: true },
      );
      if (!res?.data?.success) return [];
      return res.data.data.logs ?? [];
    },
    [tenantSlug],
  );

  // Seed: one page of the latest logs establishes the cursor so the first poll
  // doesn't re-deliver rows already shown.
  const seedLive = useCallback(async () => {
    const rows = await fetchLivePage(0);
    const sorted = [...rows].sort((a, b) => (b.id || 0) - (a.id || 0));
    liveCursorRef.current = sorted.length ? sorted[0].id || 0 : 0;
    setLiveRows(sorted.slice(0, LIVE_CAP));
  }, [fetchLivePage]);

  const pollLive = useCallback(async () => {
    const rows = await fetchLivePage(liveCursorRef.current);
    if (rows.length === 0) return;
    const sorted = [...rows].sort((a, b) => (b.id || 0) - (a.id || 0));
    liveCursorRef.current = Math.max(liveCursorRef.current, sorted[0].id || 0);
    // Prepend newest, then clamp to the buffer cap (drop oldest).
    setLiveRows((prev) => [...sorted, ...prev].slice(0, LIVE_CAP));
  }, [fetchLivePage]);

  useEffect(() => {
    if (tab !== 'live' || !tenantSlug || !liveOn) return undefined;
    let intervalId = null;
    let cancelled = false;
    (async () => {
      if (!liveSeededRef.current) {
        await seedLive();
        liveSeededRef.current = true;
      }
      if (cancelled) return;
      intervalId = setInterval(pollLive, LIVE_POLL_MS);
    })();
    return () => {
      cancelled = true;
      if (intervalId) clearInterval(intervalId);
    };
  }, [tab, tenantSlug, liveOn, seedLive, pollLive]);

  // Re-seed fresh on the next entry once the user leaves the live tab.
  useEffect(() => {
    if (tab !== 'live') liveSeededRef.current = false;
  }, [tab]);

  const applyFilters = () => {
    setPage(1);
    fetchLogs(1, filterModel, filterToken, filterStart, filterEnd);
    fetchStat(filterModel, filterToken, filterStart, filterEnd);
  };

  const goPage = (next) => {
    setPage(next);
    fetchLogs(next, filterModel, filterToken, filterStart, filterEnd);
  };

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const selectedLog = logs[selRow];

  const inputStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 11,
    padding: '4px 8px',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
  };

  return (
    <HFShell
      active='logs'
      crumbs={[
        tr('console.nav.section_my_account', 'my account'),
        tr('console.log.crumb', 'usage & logs'),
      ]}
      actions={
        <>
          <span className='muted mono' style={{ fontSize: 11 }}>
            {loading
              ? tr('console.common.loading', 'loading…')
              : tr('console.log.requests_count', '{{count}} requests', {
                  count: total,
                })}
          </span>
        </>
      }
    >
      {/* Filter bar */}
      <div
        style={{
          padding: '10px 28px',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
          flexWrap: 'wrap',
        }}
      >
        <input
          style={{ ...inputStyle, width: 160 }}
          placeholder={tr('console.log.ph_model', 'model name…')}
          value={filterModel}
          onChange={(e) => setFilterModel(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && applyFilters()}
        />
        <input
          style={{ ...inputStyle, width: 140 }}
          placeholder={tr('console.log.ph_token', 'token name…')}
          value={filterToken}
          onChange={(e) => setFilterToken(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && applyFilters()}
        />
        <input
          style={{ ...inputStyle, width: 160 }}
          type='datetime-local'
          title={tr('console.log.start_time', 'start time')}
          value={filterStart}
          onChange={(e) => setFilterStart(e.target.value)}
        />
        <span className='muted' style={{ fontSize: 11 }}>
          →
        </span>
        <input
          style={{ ...inputStyle, width: 160 }}
          type='datetime-local'
          title={tr('console.log.end_time', 'end time')}
          value={filterEnd}
          onChange={(e) => setFilterEnd(e.target.value)}
        />
        <button type='button' className='btn primary' onClick={applyFilters}>
          {tr('console.common.search', 'search')}
        </button>
        <button
          type='button'
          className='btn ghost'
          onClick={() => {
            setFilterModel('');
            setFilterToken('');
            setFilterStart('');
            setFilterEnd('');
            setPage(1);
            fetchLogs(1, '', '', '', '');
            fetchStat('', '', '', '');
          }}
        >
          {tr('console.log.clear', 'clear')}
        </button>
        {tab === 'trace' && (
          <button
            type='button'
            className='btn ghost'
            data-testid='log-export-btn'
            onClick={() => {
              const params = new URLSearchParams();
              if (filterModel) params.set('model_name', filterModel);
              if (filterToken) params.set('token_name', filterToken);
              if (filterStart)
                params.set(
                  'start_time',
                  String(Math.floor(new Date(filterStart).getTime() / 1000)),
                );
              if (filterEnd)
                params.set(
                  'end_time',
                  String(Math.floor(new Date(filterEnd).getTime() / 1000)),
                );
              const qs = params.toString();
              window.location.href =
                `/api/v2/${tenantSlug}/logs/export` + (qs ? `?${qs}` : '');
            }}
          >
            📥 {tr('console.log.export_csv', 'export CSV')}
          </button>
        )}
      </div>

      {/* Aggregate stat header — GET /logs/stat over the active filters.
          requests/quota reflect the full filter window; rpm/tpm are rolling
          last-60s rates. Honest — until the first successful fetch. */}
      <div
        data-testid='log-stat-header'
        style={{
          display: 'flex',
          gap: 30,
          padding: '10px 28px',
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
          flexWrap: 'wrap',
        }}
      >
        {[
          [
            tr('console.log.stat_requests', 'requests'),
            stat ? Number(stat.total_requests ?? 0).toLocaleString() : '—',
            tr('console.log.in_window', 'in window'),
          ],
          [
            tr('console.log.stat_quota', 'quota'),
            stat
              ? `$${(Number(stat.total_quota ?? 0) / getQuotaPerUSD()).toFixed(4)}`
              : '—',
            tr('console.log.in_window', 'in window'),
          ],
          [
            tr('console.log.stat_rpm', 'rpm'),
            stat ? Number(stat.rpm ?? 0).toLocaleString() : '—',
            tr('console.log.last_60s', 'last 60s'),
          ],
          [
            tr('console.log.stat_tpm', 'tpm'),
            stat ? Number(stat.tpm ?? 0).toLocaleString() : '—',
            tr('console.log.last_60s', 'last 60s'),
          ],
        ].map(([l, v, sub]) => (
          <div key={l}>
            <div className='lbl'>
              {l}
              <span className='faint' style={{ marginLeft: 5 }}>
                · {sub}
              </span>
            </div>
            <div className='display' style={{ fontSize: 20, marginTop: 2 }}>
              {statLoading ? '…' : v}
            </div>
          </div>
        ))}
      </div>

      {/* Tabs */}
      <div
        style={{
          display: 'flex',
          padding: '0 28px',
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
        }}
      >
        {[
          ['trace', tr('console.log.tab_requests', 'Requests'), total || ''],
          ['cluster', tr('console.log.tab_clusters', 'Error clusters'), '—'],
          ['live', tr('console.log.tab_live', 'Live tail'), '—'],
        ].map(([k, l, c]) => (
          <button
            key={k}
            type='button'
            onClick={() => setTab(k)}
            style={{
              padding: '12px 16px',
              background: 'transparent',
              border: 0,
              cursor: 'pointer',
              fontFamily: 'var(--hf-mono)',
              fontSize: 11,
              color: tab === k ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
              borderBottom:
                tab === k
                  ? '2px solid var(--hf-accent)'
                  : '2px solid transparent',
              marginBottom: -1,
              display: 'flex',
              gap: 8,
              alignItems: 'center',
            }}
          >
            {l}
            <span style={{ fontSize: 9, color: 'var(--hf-ink-4)' }}>{c}</span>
          </button>
        ))}
      </div>

      {/* ── Trace tab ── */}
      {tab === 'trace' && (
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            height: 'calc(100vh - 270px)',
          }}
        >
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: '1.5fr 1fr',
              minHeight: 0,
              flex: 1,
            }}
          >
            {/* Log table */}
            <div
              style={{
                borderRight: '1px solid var(--hf-rule)',
                overflow: 'auto',
              }}
            >
              {loading && (
                <div style={{ padding: '10px 22px' }}>
                  <HfSkeletonRows rows={6} />
                </div>
              )}

              {!loading && logs.length === 0 && (
                <div
                  className='muted'
                  style={{ padding: '20px 22px', fontSize: 12 }}
                >
                  {tr('console.log.empty', 'No logs found.')}
                </div>
              )}

              {!loading && logs.length > 0 && (
                <div className='hf-table-scroll'>
                  <table className='t'>
                    <thead>
                      <tr>
                        <th>{tr('console.log.th_timestamp', 'timestamp')}</th>
                        <th>{tr('console.log.th_dur', 'dur')}</th>
                        <th>{tr('console.log.th_ttft', 'ttft')}</th>
                        <th>{tr('console.log.th_model', 'model')}</th>
                        <th>{tr('console.log.th_upstream', 'upstream')}</th>
                        <th>{tr('console.log.th_token', 'token')}</th>
                        <th>{tr('console.log.th_tok', 'tok')}</th>
                        <th>$</th>
                        <th>{tr('console.log.th_code', 'code')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {logs.map((r, i) => (
                        <tr
                          key={r.id ?? i}
                          onClick={() => setSelRow(i)}
                          style={{
                            background:
                              selRow === i ? 'var(--hf-sunken)' : undefined,
                            cursor: 'pointer',
                            borderLeft:
                              selRow === i
                                ? '2px solid var(--hf-accent)'
                                : '2px solid transparent',
                          }}
                        >
                          <td className='mono muted'>
                            {fmtTime(r.created_at)}
                          </td>
                          <td className='mono'>
                            {r.total_latency_ms ?? '—'}
                            {r.total_latency_ms != null && (
                              <span className='faint'>ms</span>
                            )}
                          </td>
                          <td className='mono'>
                            <NotAvailable
                              reason={tr(
                                'console.log.ttft_na_reason',
                                'TTFT not stored: the log schema has no time-to-first-token column',
                              )}
                            />
                          </td>
                          <td className='strong'>{r.model_name || '—'}</td>
                          <td className='mono muted'>
                            {r.channel_name ? (
                              r.channel_name
                            ) : r.channel ? (
                              `#${r.channel}`
                            ) : (
                              <NotAvailable
                                reason={tr(
                                  'console.log.upstream_na_reason',
                                  'upstream channel id not recorded on this log row',
                                )}
                              />
                            )}
                          </td>
                          <td className='mono muted'>{r.token_name || '—'}</td>
                          <td className='mono muted'>
                            {fmtTok(r.prompt_tokens, r.completion_tokens)}
                          </td>
                          <td className='mono'>{fmtCost(r.quota)}</td>
                          <td>
                            {(() => {
                              const o = outcomeTag(r);
                              return (
                                <span className={o.cls}>
                                  {tr(
                                    `console.log.outcome_${o.label}`,
                                    o.label,
                                  )}
                                </span>
                              );
                            })()}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>

            {/* Detail panel */}
            <div style={{ overflow: 'auto', background: 'var(--hf-paper)' }}>
              {selectedLog ? (
                <>
                  <div
                    style={{
                      padding: '20px 22px',
                      borderBottom: '1px solid var(--hf-rule)',
                    }}
                  >
                    <div className='lbl' style={{ marginBottom: 4 }}>
                      {tr('console.log.detail_request', 'request')} ·{' '}
                      {fmtTime(selectedLog.created_at)}
                    </div>
                    <div className='display' style={{ fontSize: 19 }}>
                      {selectedLog.model_name || '—'}
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        gap: 8,
                        marginTop: 8,
                        alignItems: 'center',
                        flexWrap: 'wrap',
                      }}
                    >
                      {(() => {
                        const o = outcomeTag(selectedLog);
                        return (
                          <span className={o.cls}>
                            {tr(`console.log.outcome_${o.label}`, o.label)}
                          </span>
                        );
                      })()}
                      {selectedLog.model_name && (
                        <span className='pill'>{selectedLog.model_name}</span>
                      )}
                      {selectedLog.total_latency_ms != null && (
                        <span className='pill'>
                          {selectedLog.total_latency_ms}ms
                        </span>
                      )}
                      {selectedLog.is_stream && (
                        <span className='pill'>
                          {tr('console.log.stream', 'stream')}
                        </span>
                      )}
                    </div>
                  </div>

                  <div style={{ padding: 20 }}>
                    <div className='lbl' style={{ marginBottom: 8 }}>
                      {tr('console.log.details', 'details')}
                    </div>
                    <div
                      className='panel-paper'
                      style={{
                        padding: 12,
                        fontFamily: 'var(--hf-mono)',
                        fontSize: 11,
                        lineHeight: 1.7,
                      }}
                    >
                      <div>
                        <span className='muted'>
                          {tr('console.log.detail_model', 'model')}:
                        </span>{' '}
                        {selectedLog.model_name || '—'}
                      </div>
                      <div>
                        <span className='muted'>
                          {tr('console.log.detail_token_name', 'token name')}:
                        </span>{' '}
                        {selectedLog.token_name || '—'}
                      </div>
                      <div>
                        <span className='muted'>
                          {tr(
                            'console.log.detail_prompt_tokens',
                            'prompt tokens',
                          )}
                          :
                        </span>{' '}
                        {selectedLog.prompt_tokens ?? '—'}
                      </div>
                      <div>
                        <span className='muted'>
                          {tr(
                            'console.log.detail_completion_tokens',
                            'completion tokens',
                          )}
                          :
                        </span>{' '}
                        {selectedLog.completion_tokens ?? '—'}
                      </div>
                      <div>
                        <span className='muted'>
                          {tr('console.log.detail_cost', 'cost')}:
                        </span>{' '}
                        {fmtCost(selectedLog.quota)}
                      </div>
                      <div>
                        <span className='muted'>
                          {tr('console.log.detail_duration', 'duration')}:
                        </span>{' '}
                        {selectedLog.total_latency_ms != null
                          ? `${selectedLog.total_latency_ms}ms`
                          : '—'}
                      </div>
                      <div>
                        <span className='muted'>
                          {tr('console.log.detail_streaming', 'streaming')}:
                        </span>{' '}
                        {selectedLog.is_stream
                          ? tr('console.log.yes', 'yes')
                          : tr('console.log.no', 'no')}
                      </div>
                      {selectedLog.content && (
                        <div>
                          <span className='muted'>
                            {tr('console.log.detail_note', 'note')}:
                          </span>{' '}
                          {selectedLog.content}
                        </div>
                      )}
                    </div>
                  </div>

                  {/* Routing trace — only present when the request failed over
                      between channels. Answers "this request was slow/failed,
                      what did the gateway actually try?", which the single
                      channel column cannot. */}
                  {parseRouteAttempts(selectedLog).length > 0 && (
                    <div
                      style={{ padding: '0 20px 20px' }}
                      data-testid='log-route-attempts'
                    >
                      <div className='lbl' style={{ marginBottom: 8 }}>
                        {tr('console.log.routing', 'routing')}
                      </div>
                      <div
                        className='panel-paper'
                        style={{
                          padding: 12,
                          fontFamily: 'var(--hf-mono)',
                          fontSize: 11,
                          lineHeight: 1.7,
                        }}
                      >
                        {parseRouteAttempts(selectedLog).map((a, i) => (
                          <div
                            key={`${a.channel_id}-${i}`}
                            data-testid={`route-attempt-${i}`}
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 8,
                              flexWrap: 'wrap',
                            }}
                          >
                            <span className='muted'>{i + 1}.</span>
                            <span className='strong'>
                              #{a.channel_id}
                              {a.channel_name ? ` ${a.channel_name}` : ''}
                            </span>
                            {a.provider && (
                              <span className='muted'>{a.provider}</span>
                            )}
                            <span className={attemptTagClass(a.outcome)}>
                              {tr(
                                `console.log.attempt_${a.outcome}`,
                                a.outcome || 'unknown',
                              )}
                            </span>
                            {a.status_code ? (
                              <span className='muted'>{a.status_code}</span>
                            ) : null}
                            {a.error_code ? (
                              <span className='faint'>{a.error_code}</span>
                            ) : null}
                            <span className='muted'>
                              {a.duration_ms != null
                                ? `${a.duration_ms}ms`
                                : ''}
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              ) : (
                !loading && (
                  <div
                    className='muted'
                    style={{ padding: '20px 22px', fontSize: 12 }}
                  >
                    {tr(
                      'console.log.select_row',
                      'Select a row to view details.',
                    )}
                  </div>
                )
              )}
            </div>
          </div>

          {/* Pagination */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 12,
              padding: '10px 28px',
              borderTop: '1px solid var(--hf-rule)',
              background: 'var(--hf-paper)',
              fontSize: 12,
            }}
          >
            <button
              type='button'
              className='btn'
              disabled={page <= 1 || loading}
              onClick={() => goPage(page - 1)}
            >
              {tr('console.log.prev', '← prev')}
            </button>
            <span className='mono muted'>
              {tr('console.log.page_of', 'page {{page}} of {{total}}', {
                page,
                total: totalPages,
              })}
            </span>
            <button
              type='button'
              className='btn'
              disabled={page >= totalPages || loading}
              onClick={() => goPage(page + 1)}
            >
              {tr('console.log.next', 'next →')}
            </button>
            <span className='muted' style={{ marginLeft: 'auto' }}>
              {tr(
                'console.log.total_per_page',
                '{{total}} total · {{size}} per page',
                { total, size: PAGE_SIZE },
              )}
            </span>
          </div>
        </div>
      )}

      {/* ── Cluster tab — wired to GET /api/v2/:slug/logs/cluster ── */}
      {/* Wave 2: Cluster tab wired; Live tail SSE deferred to v3 */}
      {tab === 'cluster' && (
        <div style={{ padding: 24 }}>
          {/* Bucket toggle */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              marginBottom: 16,
            }}
          >
            <span
              className='muted'
              style={{ fontSize: 11, fontFamily: 'var(--hf-mono)' }}
            >
              {tr('console.log.bucket_label', 'bucket:')}
            </span>
            {['hour', 'day'].map((b) => (
              <button
                key={b}
                type='button'
                className={clusterBucket === b ? 'btn primary' : 'btn'}
                style={{ fontSize: 11 }}
                onClick={() => setClusterBucket(b)}
              >
                {tr(`console.log.bucket_${b}`, b)}
              </button>
            ))}
            {clusterLoading && (
              <span
                className='muted'
                style={{ fontSize: 11, fontFamily: 'var(--hf-mono)' }}
              >
                {tr('console.common.loading', 'loading…')}
              </span>
            )}
          </div>

          {/* Cluster table */}
          {!clusterLoading && clusterItems.length === 0 && (
            <div className='muted' style={{ padding: '20px 0', fontSize: 12 }}>
              {tr(
                'console.log.cluster_empty',
                'No cluster data for selected period.',
              )}
            </div>
          )}

          {clusterItems.length > 0 && (
            <div className='hf-table-scroll'>
              <table className='t' data-testid='cluster-table'>
                <thead>
                  <tr>
                    <th>{tr('console.log.th_model', 'model')}</th>
                    <th>{tr('console.log.th_error_code', 'error code')}</th>
                    <th>{tr('console.log.th_bucket', 'bucket')}</th>
                    <th>{tr('console.log.th_count', 'count')}</th>
                  </tr>
                </thead>
                <tbody>
                  {clusterItems.map((row, i) => {
                    const code = row.error_code || '';
                    const is5xx = /^5/.test(code);
                    const is4xx = /^4/.test(code);
                    return (
                      <tr key={i}>
                        <td className='strong'>{row.model_name || '—'}</td>
                        <td>
                          {code ? (
                            <span
                              className={
                                is5xx ? 'tag error' : is4xx ? 'tag warn' : 'tag'
                              }
                            >
                              {code}
                            </span>
                          ) : (
                            <span className='muted'>—</span>
                          )}
                        </td>
                        <td className='mono muted' style={{ fontSize: 10 }}>
                          {row.bucket}
                        </td>
                        <td className='mono'>{row.count}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* ── Live tail tab — cursor-poll against /logs?after_id= every 3s ── */}
      {tab === 'live' && (
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            height: 'calc(100vh - 270px)',
          }}
        >
          {/* Controls */}
          <div
            style={{
              padding: '10px 28px',
              display: 'flex',
              alignItems: 'center',
              gap: 12,
              borderBottom: '1px solid var(--hf-rule)',
              background: 'var(--hf-paper)',
            }}
          >
            <span
              data-testid='live-status'
              className={liveOn ? 'tag ok' : 'tag'}
            >
              {liveOn
                ? tr('console.log.live_polling', '● live · polling 3s')
                : tr('console.log.live_paused', '⏸ paused')}
            </span>
            <button
              type='button'
              className='btn'
              data-testid='live-pause-btn'
              onClick={() => setLiveOn((v) => !v)}
            >
              {liveOn
                ? tr('console.log.pause', 'pause')
                : tr('console.log.resume', 'resume')}
            </button>
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr(
                'console.log.live_rows',
                '{{count}} rows · newest first · cap {{cap}}',
                { count: liveRows.length, cap: LIVE_CAP },
              )}
            </span>
          </div>

          {/* Live table — reuses the trace columns (incl. honest TTFT n/a). */}
          <div style={{ overflow: 'auto', flex: 1 }}>
            {liveRows.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 22px', fontSize: 12 }}
              >
                {liveOn
                  ? tr('console.log.live_waiting', 'Waiting for live requests…')
                  : tr(
                      'console.log.live_paused_hint',
                      'Paused — resume to continue tailing.',
                    )}
              </div>
            ) : (
              <div className='hf-table-scroll'>
                <table className='t' data-testid='live-table'>
                  <thead>
                    <tr>
                      <th>{tr('console.log.th_timestamp', 'timestamp')}</th>
                      <th>{tr('console.log.th_dur', 'dur')}</th>
                      <th>{tr('console.log.th_ttft', 'ttft')}</th>
                      <th>{tr('console.log.th_model', 'model')}</th>
                      <th>{tr('console.log.th_upstream', 'upstream')}</th>
                      <th>{tr('console.log.th_token', 'token')}</th>
                      <th>{tr('console.log.th_tok', 'tok')}</th>
                      <th>$</th>
                      <th>{tr('console.log.th_code', 'code')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {liveRows.map((r, i) => {
                      const o = outcomeTag(r);
                      return (
                        <tr key={r.id ?? i}>
                          <td className='mono muted'>
                            {fmtTime(r.created_at)}
                          </td>
                          <td className='mono'>
                            {r.total_latency_ms ?? '—'}
                            {r.total_latency_ms != null && (
                              <span className='faint'>ms</span>
                            )}
                          </td>
                          <td className='mono'>
                            <NotAvailable
                              reason={tr(
                                'console.log.ttft_na_reason',
                                'TTFT not stored: the log schema has no time-to-first-token column',
                              )}
                            />
                          </td>
                          <td className='strong'>{r.model_name || '—'}</td>
                          <td className='mono muted'>
                            {r.channel_name ? (
                              r.channel_name
                            ) : r.channel ? (
                              `#${r.channel}`
                            ) : (
                              <NotAvailable
                                reason={tr(
                                  'console.log.upstream_na_reason',
                                  'upstream channel id not recorded on this log row',
                                )}
                              />
                            )}
                          </td>
                          <td className='mono muted'>{r.token_name || '—'}</td>
                          <td className='mono muted'>
                            {fmtTok(r.prompt_tokens, r.completion_tokens)}
                          </td>
                          <td className='mono'>{fmtCost(r.quota)}</td>
                          <td>
                            <span className={o.cls}>
                              {tr(`console.log.outcome_${o.label}`, o.label)}
                            </span>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}
    </HFShell>
  );
};

export default HFLog;
