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
import HfSkeletonRows from '../../../components/hifi/HfSkeletonRows';
import HfUsageTrendChart from '../../../components/hifi/HfUsageTrendChart';
import { API, getServerAddress } from '../../../helpers';
import {
  getQuotaPerUSD,
  quotaToUSD,
  formatUSD,
} from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import {
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

// GET /api/v2/:slug/logs rejects page_size > 100 by falling back to 20
// (v2_log.go), so asking for more than this returns *fewer* rows, not more.
const DASHBOARD_LOG_PAGE_SIZE = 100;

// GetUserQuotaDates (internal/adapter/handler/usedata.go:41) answers
// HTTP 200 with success:false when end-start exceeds 2,592,000s (30 days).
// 29 days keeps every request inside that ceiling with a day of margin for
// the gap between this browser's clock and the server's.
const DASHBOARD_TREND_WINDOW_SECONDS = 29 * 24 * 60 * 60;
const DAY_SECONDS = 24 * 60 * 60;

// /api/status ships announcements/faq/api_info as JSON arrays already decoded
// server-side (console_setting.Get{Announcements,FAQ,ApiInfo}), but an older
// deploy or a hand-edited option could still hand this a raw JSON string —
// parse defensively so a malformed console-setting option degrades to "panel
// absent", never a blank/crashed dashboard.
const asList = (val) => {
  if (Array.isArray(val)) return val;
  if (typeof val === 'string' && val.trim()) {
    try {
      const parsed = JSON.parse(val);
      return Array.isArray(parsed) ? parsed : [];
    } catch (_) {
      return [];
    }
  }
  return [];
};

// Shown when the user has zero tokens — Reseller-MVP onboarding TTFT lift,
// modelled on OpenRouter / Anthropic quickstart pattern.
//
// The base URL was the module constant 'https://api.lurus.cn/v1' — a domain
// retired in 2026-04 that no longer resolves, i.e. the very first command we
// hand a brand-new customer could not succeed. It now resolves per render from
// getServerAddress() (configured `status.server_address`, else the origin the
// console is served from), so the quickstart names the host the reader is
// already talking to. See web/src/pages/v2/Token/index.jsx for the same fix.
const OnboardingCurlBlock = ({ username, tenantSlug }) => {
  const { t } = useTranslation();
  const relayBaseUrl = useMemo(
    () => `${getServerAddress().replace(/\/+$/, '')}/v1`,
    [],
  );
  const navigateToTokens = (e) => {
    e.preventDefault();
    window.location.href = '/console/v2/token';
  };
  const curlExample = `curl ${relayBaseUrl}/chat/completions \\
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
  // Server-reported row count for the window; `logs` is only the first page.
  const [logTotal, setLogTotal] = useState(null);
  const [loading, setLoading] = useState(true);

  // /api/data/self/ — per (user, model, hour) quota rows for the trailing
  // DASHBOARD_TREND_WINDOW_SECONDS window (usedata.go:141 GetQuotaDataByUserId).
  const [quotaRows, setQuotaRows] = useState([]);
  const [quotaWindow, setQuotaWindow] = useState({ start: 0, end: 0 });
  const [quotaLoaded, setQuotaLoaded] = useState(false);

  // /api/status — the console-setting panels it already returns and the v2
  // dashboard previously discarded (misc.go:178-210).
  const [statusData, setStatusData] = useState(null);

  // /api/uptime/status — empty array when no groups are configured (uptime_kuma.go:131).
  const [uptimeGroups, setUptimeGroups] = useState([]);

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
          `/api/v2/${tenantSlug}/logs?page=1&page_size=${DASHBOARD_LOG_PAGE_SIZE}&start_time=${startTime}`,
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
        const total = logsRes.data.data?.total;
        setLogTotal(typeof total === 'number' ? total : null);
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

  // /api/data/self/ is UserAuth (api-router.go:115, middleware.UserAuth());
  // /api/status and /api/uptime/status are public — registered with no
  // middleware under the "Public routes" block (api-router.go:24-32). None
  // of the three are under /api/v2/:slug/, so unlike fetchData this does not
  // wait on tenantSlug — it fires once on mount and again on "refresh".
  const fetchExtras = useCallback(async () => {
    const end = Math.floor(Date.now() / 1000);
    const start = end - DASHBOARD_TREND_WINDOW_SECONDS;
    setQuotaWindow({ start, end });
    // Promise.allSettled, not Promise.all: these three calls feed five
    // independent panels. A rejection on any one of them must not blank the
    // other two's panels — each result below is branched on its own settled
    // outcome instead of a shared try/catch.
    const [quotaResult, statusResult, uptimeResult] = await Promise.allSettled([
      // No skipErrorHandler here: /api/data/self/ is session-authed, and a
      // stale session must go through the same Layer-C 401 self-heal +
      // replay as /api/v2/:slug/user/me (helpers/api.js:110-133). The
      // >30-day span refusal (usedata.go:41) is HTTP 200 success:false, so
      // it never reaches that interceptor — it's handled by the `success`
      // check below, same as before.
      API.get(`/api/data/self/?start_timestamp=${start}&end_timestamp=${end}`),
      API.get('/api/status', { skipErrorHandler: true }),
      API.get('/api/uptime/status', { skipErrorHandler: true }),
    ]);

    const quotaRes =
      quotaResult.status === 'fulfilled' ? quotaResult.value : null;
    const rows = quotaRes?.data?.success ? quotaRes.data.data : null;
    setQuotaRows(Array.isArray(rows) ? rows : []);
    setQuotaLoaded(true);

    const statusRes =
      statusResult.status === 'fulfilled' ? statusResult.value : null;
    if (statusRes?.data?.success && statusRes.data.data) {
      setStatusData(statusRes.data.data);
    }

    const uptimeRes =
      uptimeResult.status === 'fulfilled' ? uptimeResult.value : null;
    const groups = uptimeRes?.data?.success ? uptimeRes.data.data : null;
    setUptimeGroups(Array.isArray(groups) ? groups : []);
  }, []);

  useEffect(() => {
    fetchExtras();
  }, [fetchExtras]);

  // Derive spend KPI from user/me
  const spendUSD = me ? parseFloat(quotaToUSD(me.used_quota ?? 0)) : null;
  const remainUSD =
    me && me.remaining_quota != null
      ? me.remaining_quota < 0
        ? '∞'
        : `$${quotaToUSD(me.remaining_quota)}`
      : null;

  // Realtime KPIs derived from the fetched 5-minute window.
  // QPS counts every request in the window, so it must use the server's
  // `total`; logs.length is one page and would cap the rate at
  // DASHBOARD_LOG_PAGE_SIZE / window.
  const windowRequests = logTotal ?? logs.length;
  const qps =
    windowRequests > 0 ? windowRequests / DASHBOARD_REALTIME_WINDOW_SECONDS : 0;
  const p50 = computeLatencyP50(logs);
  const p95 = computeLatencyP95(logs);
  const p99 = computeLatencyP99(logs);
  const errorRate = computeErrorRate(logs);
  const costByModel = computeCostByModel(logs).slice(0, 6);
  const hasRealtimeData = logs.length > 0;
  const showOnboarding = !loading && me && (me.token_count ?? 0) === 0;

  // Activity table uses the most-recent slice only.
  const recentLogs = pickRecent(logs, 5);

  // ── /api/data/self/ derived panels ──────────────────────────────────────
  // Dense per-day series across the full requested window (zero-fill days
  // with no rows) so the trend reflects the window even when traffic is
  // sparse, rather than only the days a row happens to exist for.
  //
  // Buckets key on the BROWSER's local calendar day, not the UTC day: this
  // bar's label is rendered by HfUsageTrendChart via
  // `new Date(day*1000).toLocaleDateString(...)`, i.e. in local time. Keying
  // on UTC days would silently offset every bucket by the browser's UTC
  // offset — e.g. a UTC+8 reader's 00:00–08:00 local traffic would land in
  // the *previous* UTC day's bucket, so "today"'s bar would miss the first
  // 8 hours of today and "yesterday"'s bar would carry them instead. Using
  // `setDate` (not a fixed 86400s step) to walk the window keeps this correct
  // across a DST transition, where a local day is not always 24h long.
  const localDayKey = (tsSeconds) => {
    const d = new Date(tsSeconds * 1000);
    d.setHours(0, 0, 0, 0);
    return Math.floor(d.getTime() / 1000);
  };
  const trendByDay = useMemo(() => {
    if (!quotaWindow.start || !quotaWindow.end) return [];
    const totals = new Map();
    for (const row of quotaRows) {
      const ts = Number(row?.created_at) || 0;
      if (!ts) continue;
      const day = localDayKey(ts);
      totals.set(day, (totals.get(day) || 0) + (Number(row?.quota) || 0));
    }
    const endDay = localDayKey(quotaWindow.end);
    const cursor = new Date(quotaWindow.start * 1000);
    cursor.setHours(0, 0, 0, 0);
    const days = [];
    let day = Math.floor(cursor.getTime() / 1000);
    while (day <= endDay) {
      days.push({ day, quota: totals.get(day) || 0 });
      cursor.setDate(cursor.getDate() + 1);
      day = Math.floor(cursor.getTime() / 1000);
    }
    return days;
  }, [quotaRows, quotaWindow]);
  const trendTotalQuota = trendByDay.reduce((sum, d) => sum + d.quota, 0);
  const hasUsageData = quotaLoaded && quotaRows.length > 0;

  const modelDistribution = useMemo(() => {
    const totals = new Map();
    for (const row of quotaRows) {
      const model = row?.model_name || '—';
      totals.set(model, (totals.get(model) || 0) + (Number(row?.quota) || 0));
    }
    return Array.from(totals.entries())
      .map(([model, quota]) => ({ model, quota }))
      .sort((a, b) => b.quota - a.quota)
      .slice(0, 8);
  }, [quotaRows]);

  // /api/status ships enable_data_export (misc.go:165, common.DataExportEnabled).
  // When it's off the aggregator stops writing quota_data at all (repo/log.go:584,
  // repo/usedata.go:18) — /api/data/self/ would answer 200 with an empty array
  // forever, not "empty today". Rendering the trend/distribution panels' empty
  // state in that case would print "No usage recorded" beside a non-zero total
  // spend KPI, i.e. the console would be asserting something about data it knows
  // it isn't collecting. The legacy console gates its data-dashboard nav entry on
  // exactly this flag (SiderBar.jsx:79); these two panels do the same and render
  // nothing — not even the empty-state copy — when it's off.
  const dataExportEnabled = !!statusData?.enable_data_export;
  const showUsageTrend = dataExportEnabled;
  const showModelDistribution = dataExportEnabled;

  // ── /api/status derived panels — each renders nothing when its own
  // *_enabled flag is false or the parsed payload is empty (misc.go:178-210).
  const announcementsList = asList(statusData?.announcements);
  const showAnnouncements =
    !!statusData?.announcements_enabled && announcementsList.length > 0;

  const faqList = asList(statusData?.faq);
  const showFaq = !!statusData?.faq_enabled && faqList.length > 0;

  const apiInfoList = asList(statusData?.api_info);
  const showApiInfo = !!statusData?.api_info_enabled && apiInfoList.length > 0;

  // /api/uptime/status returns [] when unconfigured regardless of the flag
  // (uptime_kuma.go:131); gate on both so a stale flag never surfaces a panel
  // with nothing in it, and a configured-but-disabled panel stays hidden.
  const showUptime =
    !!statusData?.uptime_kuma_enabled && uptimeGroups.length > 0;

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
          <button
            type='button'
            className='btn'
            onClick={() => {
              fetchData();
              fetchExtras();
            }}
          >
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
                  // Live rate, like every other money figure on this page.
                  const usd = (row.totalQuota / getQuotaPerUSD()).toFixed(4);
                  return (
                    // Deep-link into the log page pre-filtered to this model:
                    // the cost figure stops being a dead end and becomes the
                    // entry point to the requests behind it.
                    <div
                      key={row.model}
                      data-testid={`cost-model-row-${i}`}
                      role='link'
                      tabIndex={0}
                      title={t(
                        'console.dashboard.view_logs',
                        'view these requests in logs',
                      )}
                      style={{ cursor: 'pointer' }}
                      onClick={() => {
                        window.location.href = `/console/v2/log?model_name=${encodeURIComponent(row.model)}`;
                      }}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter')
                          window.location.href = `/console/v2/log?model_name=${encodeURIComponent(row.model)}`;
                      }}
                    >
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

        {/* ── Usage trend · /api/data/self/ (up to 29 days) ──
            Gated on enable_data_export (see dataExportEnabled above) — absent
            entirely, not an empty state, when it's off. */}
        {showUsageTrend && (
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
                <div className='lbl'>{t('console.dashboard.usage_trend')}</div>
                <div className='display' style={{ fontSize: 18, marginTop: 2 }}>
                  {hasUsageData
                    ? formatUSD(trendTotalQuota)
                    : t('console.dashboard.no_consume')}
                </div>
              </div>
              <span className='faint mono' style={{ fontSize: 10 }}>
                {t('console.dashboard.usage_trend_window', {
                  days: DASHBOARD_TREND_WINDOW_SECONDS / DAY_SECONDS,
                })}
              </span>
            </div>
            {!quotaLoaded && <HfSkeletonRows rows={3} />}
            {quotaLoaded && !hasUsageData && (
              <div
                className='muted'
                style={{
                  fontSize: 11,
                  fontFamily: 'var(--hf-mono)',
                  padding: '24px 0',
                  textAlign: 'center',
                }}
              >
                {t('console.dashboard.usage_trend_empty')}
              </div>
            )}
            {hasUsageData && (
              <HfUsageTrendChart days={trendByDay} formatValue={formatUSD} />
            )}
          </div>
        )}

        {/* ── Model consumption distribution · /api/data/self/ ──
            Gated on enable_data_export — same reasoning as the trend panel
            above. */}
        {showModelDistribution && (
          <div className='panel' style={{ gridColumn: 'span 5', padding: 18 }}>
            <div className='lbl' style={{ marginBottom: 10 }}>
              {t('console.dashboard.model_distribution')}
            </div>
            {!quotaLoaded && <HfSkeletonRows rows={3} />}
            {quotaLoaded && modelDistribution.length === 0 && (
              <div
                className='muted'
                style={{
                  fontSize: 11,
                  fontFamily: 'var(--hf-mono)',
                  padding: '24px 0',
                  textAlign: 'center',
                }}
              >
                {t('console.dashboard.model_distribution_empty')}
              </div>
            )}
            {modelDistribution.length > 0 && (
              <div
                style={{ display: 'flex', flexDirection: 'column', gap: 10 }}
              >
                {(() => {
                  const maxQuota = modelDistribution[0].quota || 1;
                  return modelDistribution.map((row, i) => {
                    const pct = (row.quota / maxQuota) * 100;
                    return (
                      <div key={row.model} data-testid={`model-dist-row-${i}`}>
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
                            {formatUSD(row.quota)}
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
        )}

        {/* ── Announcements · /api/status (announcements_enabled) ──
            Renders nothing when the flag is off or the list is empty —
            never a placeholder, per the console-completion cycle rule that
            an absent feature must never render UI a customer sees. */}
        {showAnnouncements && (
          <div
            className='panel'
            data-testid='dash-announcements-panel'
            style={{ gridColumn: 'span 4', padding: 18 }}
          >
            <div className='lbl' style={{ marginBottom: 10 }}>
              {t('console.dashboard.announcements')}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {announcementsList.slice(0, 5).map((item, i) => (
                <div
                  key={i}
                  style={{
                    paddingBottom: 8,
                    borderBottom:
                      i < Math.min(announcementsList.length, 5) - 1
                        ? '1px dashed var(--hf-rule)'
                        : 0,
                  }}
                >
                  {item?.publishDate && (
                    <div className='mono muted' style={{ fontSize: 9 }}>
                      {fmtTs(new Date(item.publishDate).getTime())}
                    </div>
                  )}
                  <div style={{ fontSize: 12, marginTop: 2 }}>
                    {item?.content || ''}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* ── FAQ · /api/status (faq_enabled) ── */}
        {showFaq && (
          <div
            className='panel'
            data-testid='dash-faq-panel'
            style={{ gridColumn: 'span 4', padding: 18 }}
          >
            <div className='lbl' style={{ marginBottom: 10 }}>
              {t('console.dashboard.faq')}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {faqList.slice(0, 6).map((item, i) => (
                <div key={i}>
                  <div style={{ fontSize: 12, fontWeight: 600 }}>
                    {item?.question || ''}
                  </div>
                  <div className='muted' style={{ fontSize: 11, marginTop: 2 }}>
                    {item?.answer || ''}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* ── API info · /api/status (api_info_enabled) ── */}
        {showApiInfo && (
          <div
            className='panel'
            data-testid='dash-apiinfo-panel'
            style={{ gridColumn: 'span 4', padding: 18 }}
          >
            <div className='lbl' style={{ marginBottom: 10 }}>
              {t('console.dashboard.api_info')}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {apiInfoList.slice(0, 6).map((item, i) => (
                <a
                  key={i}
                  href={item?.url || '#'}
                  target='_blank'
                  rel='noreferrer'
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    gap: 8,
                    fontSize: 11,
                    textDecoration: 'none',
                    color: 'inherit',
                  }}
                >
                  <span className='mono'>{item?.route || item?.url}</span>
                  <span
                    className='muted'
                    style={{
                      fontSize: 10,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {item?.description || ''}
                  </span>
                </a>
              ))}
            </div>
          </div>
        )}

        {/* ── Uptime · GET /api/uptime/status (uptime_kuma_enabled) ── */}
        {showUptime && (
          <div
            className='panel'
            data-testid='dash-uptime-panel'
            style={{ gridColumn: 'span 12', padding: 18 }}
          >
            <div className='lbl' style={{ marginBottom: 10 }}>
              {t('console.dashboard.uptime')}
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 20 }}>
              {uptimeGroups.map((group, gi) => (
                <div key={gi} style={{ minWidth: 180 }}>
                  <div
                    className='mono muted'
                    style={{ fontSize: 10, marginBottom: 6 }}
                  >
                    {group?.categoryName || ''}
                  </div>
                  <div
                    style={{ display: 'flex', flexDirection: 'column', gap: 4 }}
                  >
                    {(group?.monitors || []).map((mon, mi) => (
                      <div
                        key={mi}
                        data-testid={`uptime-monitor-${gi}-${mi}`}
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          fontSize: 11,
                        }}
                      >
                        <span className='mono'>{mon?.name || ''}</span>
                        <span
                          className='mono'
                          style={{
                            color:
                              mon?.status === 1
                                ? 'var(--hf-ok)'
                                : 'var(--hf-err)',
                          }}
                        >
                          {typeof mon?.uptime === 'number'
                            ? `${(mon.uptime * 100).toFixed(1)}%`
                            : '—'}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </HFShell>
  );
};

export default HFDashboard;
