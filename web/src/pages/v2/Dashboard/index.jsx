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
import HFShell from '../../../components/hifi/HFShell';
import HfSkeletonRows from '../../../components/hifi/HfSkeletonRows';
import { API } from '../../../helpers';
import { QUOTA_PER_USD, quotaToUSD } from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import {
  computeQPS,
  computeLatencyP50,
  computeLatencyP95,
  computeLatencyP99,
  computeErrorRate,
  computeCostByModel,
  pickRecent,
  formatQPS,
  formatLatencyMs,
  formatErrorRate,
  DASHBOARD_REALTIME_WINDOW_SECONDS,
} from './kpis';

const fmtTs = (ts) => {
  if (!ts) return '—';
  try {
    return new Date(ts).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  } catch (_) {
    return ts;
  }
};

// Realtime KPI tiles derived from the last DASHBOARD_REALTIME_WINDOW_SECONDS
// window of /api/v2/{slug}/logs. No dedicated metrics endpoint exists yet;
// see _bmad-output/planning-artifacts/hardening-swarm-2026-05-18-acceptance.md.

// Shown when the user has zero tokens — Reseller-MVP onboarding TTFT lift,
// modelled on OpenRouter / Anthropic quickstart pattern.
const RELAY_BASE_URL = 'https://api.lurus.cn/v1';

const OnboardingCurlBlock = ({ username, tenantSlug }) => {
  const { t } = useTranslation();
  const navigateToTokens = (e) => {
    e.preventDefault();
    window.location.href = '/console/v2/token';
  };
  const curlExample = `curl ${RELAY_BASE_URL}/chat/completions \\
  -H "Authorization: Bearer YOUR_TOKEN_HERE" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Hello from ${username || 'lurus-hub'}!"}]
  }'`;
  return (
    <div
      role='region'
      aria-label={t('console.dashboard.onboarding_label', 'get started')}
      style={{
        margin: '14px 24px 0',
        padding: '18px 22px',
        border: '1px solid var(--hf-accent)',
        borderRadius: 2,
        background: 'var(--hf-accent-bg)',
      }}
    >
      <div
        className='lbl'
        style={{ fontSize: 11, marginBottom: 8, color: 'var(--hf-accent)' }}
      >
        {t(
          'console.dashboard.onboarding_label',
          'get started — first relay call',
        )}
      </div>
      <div className='display' style={{ fontSize: 18, marginBottom: 10 }}>
        {t('console.dashboard.onboarding_title', { slug: tenantSlug })}
      </div>
      <div
        style={{
          display: 'flex',
          gap: 10,
          marginBottom: 12,
          flexWrap: 'wrap',
        }}
      >
        <a
          href='/console/v2/token'
          onClick={navigateToTokens}
          className='btn primary'
          style={{ textDecoration: 'none', padding: '6px 14px' }}
        >
          {t('console.dashboard.onboarding_create', '+ create token')}
        </a>
        <span
          className='mono muted'
          style={{ fontSize: 11, alignSelf: 'center' }}
        >
          {t(
            'console.dashboard.onboarding_paste',
            'then paste it into the snippet below',
          )}
        </span>
      </div>
      <pre
        style={{
          background: 'var(--hf-code-bg)',
          color: 'var(--hf-code-ink)',
          padding: 14,
          margin: 0,
          fontSize: 11,
          lineHeight: 1.55,
          fontFamily: 'var(--hf-mono)',
          overflow: 'auto',
          border: '1px solid var(--hf-rule-strong)',
        }}
      >
        {curlExample}
      </pre>
    </div>
  );
};

// ─── Main page ────────────────────────────────────────────────────────────────

const HFDashboard = () => {
  const { t } = useTranslation();
  const tenantSlug = useTenantSlug();

  const [me, setMe] = useState(null);
  const [logs, setLogs] = useState([]);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(async () => {
    if (!tenantSlug) return;
    setLoading(true);
    const startTime =
      Math.floor(Date.now() / 1000) - DASHBOARD_REALTIME_WINDOW_SECONDS;
    try {
      // skipErrorHandler: a stale/absent session must NOT stack a red toast on
      // mount. The page degrades gracefully on its own — KPIs fall back to '—'
      // and the onboarding / empty states render — so a failed fetch is a calm
      // empty dashboard, not an error wall.
      const [meRes, logsRes] = await Promise.all([
        API.get(`/api/v2/${tenantSlug}/user/me`, { skipErrorHandler: true }),
        API.get(
          `/api/v2/${tenantSlug}/logs?page=1&page_size=200&start_time=${startTime}`,
          { skipErrorHandler: true },
        ),
      ]);
      if (meRes?.data?.success) setMe(meRes.data.data);
      if (logsRes?.data?.success) {
        // GET /logs returns { logs: [...] }; tolerate { items } / bare array
        // too. The prior code read only `.items`, so the 5-min realtime KPIs
        // were always empty against the real backend.
        const items = logsRes.data.data?.logs ?? logsRes.data.data?.items ?? [];
        setLogs(Array.isArray(items) ? items : []);
      }
    } catch (e) {
      // Intentionally silent — the degraded empty state IS the UX here.
    } finally {
      setLoading(false);
    }
  }, [tenantSlug]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // Derive spend KPI from user/me
  const spendUSD = me ? parseFloat(quotaToUSD(me.used_quota ?? 0)) : null;
  const remainUSD =
    me && me.remaining_quota != null
      ? me.remaining_quota < 0
        ? '∞'
        : `$${quotaToUSD(me.remaining_quota)}`
      : null;

  // Realtime KPIs derived from the fetched 5-minute window.
  const qps = computeQPS(logs, DASHBOARD_REALTIME_WINDOW_SECONDS);
  const p50 = computeLatencyP50(logs);
  const p95 = computeLatencyP95(logs);
  const p99 = computeLatencyP99(logs);
  const errorRate = computeErrorRate(logs);
  const costByModel = computeCostByModel(logs).slice(0, 6);
  const hasRealtimeData = logs.length > 0;
  const showOnboarding = !loading && me && (me.token_count ?? 0) === 0;

  // Activity table uses the most-recent slice only.
  const recentLogs = pickRecent(logs, 5);

  return (
    <HFShell
      active='dashboard'
      crumbs={['workspace', 'dashboard']}
      actions={
        <>
          <span className='muted mono' style={{ fontSize: 11 }}>
            {loading
              ? t('console.common.loading')
              : me
                ? t('console.dashboard.actions_stats', {
                    requests: me.request_count ?? 0,
                    spent: spendUSD?.toFixed(2) ?? '—',
                  })
                : ''}
          </span>
          <button type='button' className='btn' onClick={fetchData}>
            {t('console.common.refresh')}
          </button>
        </>
      }
    >
      {showOnboarding && (
        <OnboardingCurlBlock username={me?.username} tenantSlug={tenantSlug} />
      )}
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {t('console.dashboard.at_a_glance')}
          </div>
          <h1>
            {loading ? (
              t('console.common.loading')
            ) : me ? (
              <>
                {me.display_name ||
                  me.username ||
                  t('console.dashboard.your_workspace')}{' '}
                <span className='muted' style={{ fontWeight: 400 }}>
                  {t('console.dashboard.remaining_suffix', {
                    amount: remainUSD,
                  })}
                </span>
              </>
            ) : (
              t('console.dashboard.title_fallback')
            )}
          </h1>
          <div className='sub'>
            {me
              ? t('console.dashboard.sub_stats', {
                  tokens: me.token_count ?? 0,
                  requests: me.request_count ?? 0,
                })
              : t('console.dashboard.sub_fallback')}
          </div>
        </div>
      </div>

      <div
        className='hf-grid'
        style={{
          padding: 'var(--hf-sp-6)',
          gridTemplateColumns: 'repeat(12, 1fr)',
        }}
      >
        {/* ── KPI: Total spend (real) ── */}
        <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.total_spend')}</div>
          <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
            {loading ? '…' : me ? `$${spendUSD.toFixed(2)}` : '—'}
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-end',
              marginTop: 8,
            }}
          >
            <span className='mono muted' style={{ fontSize: 10 }}>
              {t('console.dashboard.all_time_quota')}
            </span>
          </div>
        </div>

        {/* ── KPI: Remaining quota (real) ── */}
        <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.remaining_quota')}</div>
          <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
            {loading ? '…' : (remainUSD ?? '—')}
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-end',
              marginTop: 8,
            }}
          >
            <span className='mono muted' style={{ fontSize: 10 }}>
              {me && me.remaining_quota >= 0
                ? t('console.dashboard.until_topup')
                : t('console.dashboard.unlimited_plan')}
            </span>
          </div>
        </div>

        {/* ── KPI: Total requests (real) ── */}
        <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.total_requests')}</div>
          <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
            {loading
              ? '…'
              : me
                ? (me.request_count ?? 0).toLocaleString()
                : '—'}
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-end',
              marginTop: 8,
            }}
          >
            <span className='mono muted' style={{ fontSize: 10 }}>
              {t('console.dashboard.all_time')}
            </span>
          </div>
        </div>

        {/* ── KPI: Active tokens (real) ── */}
        <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.active_tokens')}</div>
          <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
            {loading ? '…' : me ? (me.token_count ?? 0) : '—'}
          </div>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'flex-end',
              marginTop: 8,
            }}
          >
            <span className='mono muted' style={{ fontSize: 10 }}>
              {t('console.dashboard.in_workspace')}
            </span>
          </div>
        </div>

        {/* ── KPI: QPS (derived from last 5min of logs) ── */}
        <div className='panel' style={{ gridColumn: 'span 4', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.qps')}</div>
          <div
            className='display'
            style={{
              fontSize: 32,
              marginTop: 4,
              color: hasRealtimeData ? 'var(--hf-accent)' : 'var(--hf-ink-3)',
            }}
          >
            {loading ? '…' : hasRealtimeData ? formatQPS(qps) : '—'}
          </div>
          <div
            style={{
              marginTop: 8,
              fontSize: 10,
              color: 'var(--hf-ink-3)',
              fontFamily: 'var(--hf-mono)',
            }}
          >
            {hasRealtimeData
              ? t('console.dashboard.qps_active')
              : t('console.dashboard.qps_idle')}
          </div>
        </div>

        {/* ── KPI: Latency P50/P95/P99 (P99 anchors the SLO) ── */}
        <div className='panel' style={{ gridColumn: 'span 4', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.latency_ms')}</div>
          <div
            style={{
              marginTop: 6,
              display: 'grid',
              gridTemplateColumns: '1fr 1fr 1fr',
              gap: 8,
              alignItems: 'baseline',
            }}
          >
            {[
              ['p50', p50, 'var(--hf-ok)'],
              ['p95', p95, 'var(--hf-warn)'],
              ['p99', p99, 'var(--hf-err)'],
            ].map(([label, val, color]) => (
              <div key={label}>
                <div
                  className='display'
                  style={{
                    fontSize: 22,
                    color: val != null ? color : 'var(--hf-ink-3)',
                  }}
                >
                  {loading ? '…' : val != null ? formatLatencyMs(val) : '—'}
                </div>
                <div
                  className='mono'
                  style={{
                    fontSize: 9,
                    color: 'var(--hf-ink-3)',
                    marginTop: 2,
                  }}
                >
                  {label}
                </div>
              </div>
            ))}
          </div>
          <div
            style={{
              marginTop: 8,
              fontSize: 10,
              color: 'var(--hf-ink-3)',
              fontFamily: 'var(--hf-mono)',
            }}
          >
            {p99 != null
              ? t('console.dashboard.latency_active')
              : t('console.dashboard.latency_idle')}
          </div>
        </div>

        {/* ── KPI: Error rate (derived from log type 5 share) ── */}
        <div className='panel' style={{ gridColumn: 'span 4', padding: 18 }}>
          <div className='lbl'>{t('console.dashboard.error_rate')}</div>
          <div
            className='display'
            style={{
              fontSize: 32,
              marginTop: 4,
              color: !hasRealtimeData
                ? 'var(--hf-ink-3)'
                : errorRate > 0.05
                  ? 'var(--hf-err)'
                  : 'var(--hf-ok)',
            }}
          >
            {loading ? '…' : hasRealtimeData ? formatErrorRate(errorRate) : '—'}
          </div>
          <div
            style={{
              marginTop: 8,
              fontSize: 10,
              color: 'var(--hf-ink-3)',
              fontFamily: 'var(--hf-mono)',
            }}
          >
            {hasRealtimeData
              ? t('console.dashboard.error_rate_active')
              : t('console.dashboard.qps_idle')}
          </div>
        </div>

        {/* ── Cost by model · last 5 min (derived from /logs aggregation) ── */}
        <div className='panel' style={{ gridColumn: 'span 7', padding: 18 }}>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'baseline',
              marginBottom: 12,
            }}
          >
            <div>
              <div className='lbl'>{t('console.dashboard.cost_by_model')}</div>
              <div className='display' style={{ fontSize: 18, marginTop: 2 }}>
                {costByModel.length > 0
                  ? t('console.dashboard.models_active', {
                      count: costByModel.length,
                    })
                  : t('console.dashboard.no_consume')}
              </div>
            </div>
            <span className='faint mono' style={{ fontSize: 10 }}>
              {costByModel.length > 0
                ? t('console.dashboard.derived_logs')
                : t('console.dashboard.awaiting_traffic')}
            </span>
          </div>
          {costByModel.length === 0 && (
            <div
              className='muted'
              style={{
                fontSize: 11,
                fontFamily: 'var(--hf-mono)',
                padding: '24px 0',
                textAlign: 'center',
              }}
            >
              {t('console.dashboard.cost_empty')}
            </div>
          )}
          {costByModel.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {(() => {
                const maxQuota = costByModel[0].totalQuota || 1;
                return costByModel.map((row, i) => {
                  const pct = (row.totalQuota / maxQuota) * 100;
                  const usd = (row.totalQuota / QUOTA_PER_USD).toFixed(4);
                  return (
                    <div key={row.model}>
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          marginBottom: 3,
                          fontSize: 11,
                        }}
                      >
                        <span
                          className='mono'
                          style={{
                            color: 'var(--hf-ink)',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                            maxWidth: '60%',
                          }}
                        >
                          {row.model}
                        </span>
                        <span className='mono muted' style={{ fontSize: 10 }}>
                          ${usd} ·{' '}
                          {t('console.dashboard.req_suffix', {
                            n: row.requestCount,
                          })}
                        </span>
                      </div>
                      <div
                        style={{
                          height: 6,
                          background: 'var(--hf-sunken)',
                          borderRadius: 1,
                          overflow: 'hidden',
                        }}
                      >
                        <div
                          style={{
                            height: '100%',
                            width: pct + '%',
                            background:
                              i === 0 ? 'var(--hf-accent)' : 'var(--hf-info)',
                            transition: 'width 0.4s ease',
                          }}
                        />
                      </div>
                    </div>
                  );
                });
              })()}
            </div>
          )}
        </div>

        {/* ── Recent activity table ── */}
        <div className='panel' style={{ gridColumn: 'span 5', padding: 18 }}>
          <div className='lbl' style={{ marginBottom: 10 }}>
            {t('console.dashboard.recent_activity')}
          </div>
          {loading && <HfSkeletonRows rows={4} />}
          {!loading && recentLogs.length === 0 && (
            <div className='muted' style={{ fontSize: 12 }}>
              {t('console.dashboard.no_recent')}
            </div>
          )}
          {!loading && recentLogs.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: '1fr 1fr auto',
                  padding: '4px 0 6px',
                  borderBottom: '1px solid var(--hf-rule)',
                  marginBottom: 4,
                }}
              >
                <span className='lbl' style={{ fontSize: 10 }}>
                  {t('console.dashboard.col_time')}
                </span>
                <span className='lbl' style={{ fontSize: 10 }}>
                  {t('console.dashboard.col_model')}
                </span>
                <span
                  className='lbl'
                  style={{ fontSize: 10, textAlign: 'right' }}
                >
                  {t('console.dashboard.col_cost')}
                </span>
              </div>
              {recentLogs.map((log, i) => {
                const model =
                  log.model || log.ModelName || log.channel_name || '—';
                const cost =
                  log.quota != null ? `$${quotaToUSD(log.quota)}` : '—';
                const ts = fmtTs(log.created_at || log.CreatedAt || null);
                return (
                  <div
                    key={i}
                    style={{
                      display: 'grid',
                      gridTemplateColumns: '1fr 1fr auto',
                      padding: '7px 0',
                      borderBottom:
                        i < recentLogs.length - 1
                          ? '1px dashed var(--hf-rule)'
                          : 0,
                      fontSize: 11,
                      alignItems: 'center',
                    }}
                  >
                    <span className='mono muted' style={{ fontSize: 10 }}>
                      {ts}
                    </span>
                    <span
                      className='mono'
                      style={{
                        fontSize: 10,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        paddingRight: 6,
                      }}
                    >
                      {model}
                    </span>
                    <span className='mono strong' style={{ fontSize: 10 }}>
                      {cost}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </HFShell>
  );
};

export default HFDashboard;
