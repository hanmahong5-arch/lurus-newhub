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
import { API, showError } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

// HiFi 11 — Billing. Wired to real APIs (2026-05-19):
//   GET /api/v2/:slug/billing/invoices  → monthly spend buckets
//   GET /api/v2/user/billing/summary    → balance / MTD from platform

const fmtCNY = (v) =>
  typeof v === 'number'
    ? '¥' +
      v.toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      })
    : '—';

// `usdEq` is the translated "USD eq." unit suffix — resolved at render time
// because module scope has no i18n context.
const fmtQuota = (v, usdEq) =>
  typeof v === 'number' ? (v / getQuotaPerUSD()).toFixed(4) + ' ' + usdEq : '—';

const HFBilling = () => {
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` per the v2 console convention (avoids shadowing).
  const { t: tr } = useTranslation();

  const [invoices, setInvoices] = useState([]);
  const [summary, setSummary] = useState(null);
  const [loading, setLoading] = useState(true);
  const [recharging, setRecharging] = useState(false);
  // True when the platform billing service is unreachable (e.g. 503). Surfaced
  // as an honest banner instead of silently rendering "—" as if zero-balance.
  const [platformDown, setPlatformDown] = useState(false);

  const fetchAll = useCallback(async () => {
    if (!tenantSlug) return;
    setLoading(true);
    setPlatformDown(false);
    try {
      // allSettled so a platform-summary 503 doesn't also blank the invoices
      // table. summary uses skipErrorHandler so its failure shows the banner,
      // not a duplicate toast.
      const [invRes, sumRes] = await Promise.allSettled([
        API.get(`/api/v2/${tenantSlug}/billing/invoices`),
        API.get('/api/v2/user/billing/summary', { skipErrorHandler: true }),
      ]);

      if (invRes.status === 'fulfilled' && invRes.value?.data?.success) {
        setInvoices(invRes.value.data.data?.items ?? []);
      } else if (invRes.status === 'fulfilled') {
        showError(
          invRes.value?.data?.message ||
            tr(
              'console.billing.load_invoices_failed',
              'Failed to load invoices',
            ),
        );
      }
      // invRes rejected → the global interceptor already toasted it.

      if (sumRes.status === 'fulfilled' && sumRes.value?.data?.success) {
        setSummary(sumRes.value.data.data);
      } else if (sumRes.status === 'rejected') {
        // Platform billing service unreachable — balance/MTD can't be trusted.
        setPlatformDown(true);
      }
    } catch (_) {
      // unreachable with allSettled; kept as a defensive backstop
    } finally {
      setLoading(false);
    }
    // `tr` intentionally omitted: its identity is not stable under the test
    // i18n mock and would re-trigger the fetch on every render; the fetch
    // must run only on slug change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tenantSlug]);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  const handleRecharge = async () => {
    setRecharging(true);
    try {
      const res = await API.post(
        '/api/v2/user/billing/checkout',
        {
          amount_cny: 200,
          payment_method: 'alipay',
          return_url: window.location.href,
        },
        { skipErrorHandler: true },
      );
      if (res?.data?.success && res.data.data?.checkout_url) {
        window.location.href = res.data.data.checkout_url;
      } else {
        showError(
          res?.data?.message ||
            tr(
              'console.billing.checkout_failed',
              'Failed to create top-up order',
            ),
        );
      }
    } catch (_) {
      // Checkout reaches the same platform billing service — a failure here is
      // the same outage. Surface the honest banner rather than only a toast.
      setPlatformDown(true);
      showError(
        tr(
          'console.billing.service_unavailable',
          'Billing service temporarily unavailable — please try again later',
        ),
      );
    } finally {
      setRecharging(false);
    }
  };

  // Trend: last 6 invoices in chronological order for the bar chart.
  const trend = invoices.slice(0, 6).slice().reverse();
  const trendMax = trend.reduce((m, b) => Math.max(m, b.amount_cny ?? 0), 1);

  const balanceDisplay =
    summary?.wallet_balance_cny != null
      ? fmtCNY(summary.wallet_balance_cny)
      : '—';
  const mtdDisplay =
    summary?.mtd_spend_cny != null
      ? fmtCNY(summary.mtd_spend_cny)
      : invoices[0]
        ? fmtCNY(invoices[0].amount_cny)
        : '—';

  return (
    <HFShell
      active='billing'
      crumbs={[
        tr('console.nav.section_my_account', 'my account'),
        tr('console.billing.crumb', 'billing'),
      ]}
      actions={
        <>
          <button
            type='button'
            className='btn primary'
            onClick={handleRecharge}
            disabled={recharging}
            data-testid='billing-recharge'
          >
            {recharging
              ? tr('console.billing.processing', 'processing…')
              : tr('console.billing.recharge', '+ top up')}
          </button>
        </>
      }
    >
      {platformDown && (
        <div
          role='alert'
          data-testid='billing-platform-down'
          style={{
            margin: '14px 24px 0',
            padding: '10px 14px',
            border: '1px solid var(--hf-err)',
            borderRadius: 2,
            background: 'rgba(200,60,60,0.08)',
            color: 'var(--hf-ink-2)',
            fontFamily: 'var(--hf-mono)',
            fontSize: 11,
            lineHeight: 1.55,
          }}
        >
          <div style={{ fontWeight: 600, marginBottom: 2 }}>
            ⚠{' '}
            {tr(
              'console.billing.platform_down_title',
              'Billing temporarily unavailable',
            )}
          </div>
          <div style={{ opacity: 0.85 }}>
            {tr(
              'console.billing.platform_down_body',
              'The platform billing service is not responding. Balance and spend figures may be missing or stale; top-up is paused. Invoices below are tenant-local and unaffected.',
            )}
          </div>
        </div>
      )}
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.billing.title', 'billing')}
          </div>
          <h1>
            {loading ? '…' : mtdDisplay}{' '}
            <span className='muted' style={{ fontWeight: 400 }}>
              · {tr('console.billing.this_month', 'this month')}
            </span>
          </h1>
          <div className='sub'>
            {tr(
              'console.billing.subtitle',
              'prepaid balance · api consumption',
            )}
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 28 }}>
          {[
            [
              tr('console.billing.stat_balance', 'balance'),
              balanceDisplay,
              'var(--hf-ink)',
            ],
            [
              tr('console.billing.stat_mtd_spend', 'mtd spend'),
              mtdDisplay,
              'var(--hf-accent)',
            ],
          ].map(([l, v, col], i) => (
            <div key={i}>
              <div className='lbl'>{l}</div>
              <div
                className='display'
                style={{ fontSize: 26, color: col, marginTop: 2 }}
              >
                {loading ? '…' : v}
              </div>
            </div>
          ))}
        </div>
      </div>

      <div
        style={{
          padding: 24,
          display: 'grid',
          gridTemplateColumns: '1fr 360px',
          gap: 18,
        }}
      >
        {/* Invoice table */}
        <div className='panel'>
          <div
            style={{
              padding: '14px 18px',
              borderBottom: '1px solid var(--hf-rule)',
            }}
          >
            <div className='lbl'>
              {tr('console.billing.invoices_monthly', 'invoices · monthly')}
            </div>
          </div>

          {loading ? (
            <div
              style={{
                padding: 24,
                textAlign: 'center',
                color: 'var(--hf-ink-2)',
              }}
            >
              {tr('console.common.loading', 'loading…')}
            </div>
          ) : invoices.length === 0 ? (
            <div
              style={{
                padding: 24,
                textAlign: 'center',
                color: 'var(--hf-ink-2)',
              }}
            >
              {tr('console.billing.no_invoices', 'no invoice data')}
            </div>
          ) : (
            <div className='hf-table-scroll'>
              <table className='t'>
                <thead>
                  <tr>
                    <th>{tr('console.billing.th_period', 'period')}</th>
                    <th>
                      {tr('console.billing.th_amount_cny', 'amount (CNY)')}
                    </th>
                    <th>{tr('console.billing.th_quota_used', 'quota used')}</th>
                    <th>{tr('console.billing.th_requests', 'requests')}</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {invoices.map((inv, i) => (
                    <tr key={i}>
                      <td className='mono strong'>{inv.month}</td>
                      <td>
                        <span className='display' style={{ fontSize: 16 }}>
                          {fmtCNY(inv.amount_cny)}
                        </span>
                      </td>
                      <td className='mono muted'>
                        {fmtQuota(
                          inv.quota,
                          tr('console.billing.usd_eq', 'USD eq.'),
                        )}
                      </td>
                      <td className='mono muted'>{inv.request_count ?? '—'}</td>
                      <td>
                        {/* Download PDF — deferred to Phase 2 */}
                        <button
                          type='button'
                          className='btn ghost sm'
                          data-testid={`billing-download-${i}`}
                          disabled
                          title={tr(
                            'console.billing.pdf_deferred',
                            'invoice PDF generation deferred — see Phase 2 backlog',
                          )}
                        >
                          PDF
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Side panel */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {/* Payment method — edit deferred */}
          <div className='panel' style={{ padding: 18 }}>
            <div className='lbl'>
              {tr('console.billing.payment_method', 'payment method')}
            </div>
            <div
              className='panel-paper'
              style={{
                marginTop: 10,
                padding: 14,
                display: 'flex',
                alignItems: 'center',
                gap: 10,
              }}
            >
              <div
                style={{
                  width: 36,
                  height: 24,
                  background: 'var(--hf-ink)',
                  color: 'var(--hf-bg)',
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 9,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  letterSpacing: '0.1em',
                }}
              >
                ALI
              </div>
              <div>
                <div className='mono strong'>
                  {tr('console.billing.alipay', 'Alipay')}
                </div>
                <div className='faint mono' style={{ fontSize: 10 }}>
                  {tr('console.billing.default_method', 'default')}
                </div>
              </div>
            </div>
            <div style={{ marginTop: 8 }}>
              {/* Payment method management lives on Platform — link out */}
              <a
                href='https://identity.lurus.cn/wallet'
                target='_blank'
                rel='noopener noreferrer'
                className='btn sm'
                data-testid='billing-edit-payment'
                style={{ textDecoration: 'none', display: 'inline-block' }}
                title={tr(
                  'console.billing.manage_payment_title',
                  'payment method management lives on identity.lurus.cn (Platform) — link out instead',
                )}
              >
                {tr('console.billing.manage_payment', 'manage payment')} ↗
              </a>
            </div>
          </div>

          {/* Spend trend */}
          <div className='panel' style={{ padding: 18 }}>
            <div className='lbl'>
              {tr('console.billing.trend_title', 'trend · last 6 months')}
            </div>
            {loading ? (
              <div
                style={{
                  height: 100,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  color: 'var(--hf-ink-2)',
                }}
              >
                {tr('console.common.loading', 'loading…')}
              </div>
            ) : (
              <div
                style={{
                  display: 'flex',
                  alignItems: 'flex-end',
                  gap: 8,
                  height: 100,
                  marginTop: 14,
                }}
              >
                {trend.map((b, i) => (
                  <div key={i} style={{ flex: 1, textAlign: 'center' }}>
                    <div
                      style={{
                        height: ((b.amount_cny ?? 0) / trendMax) * 80 + 'px',
                        background:
                          i === trend.length - 1
                            ? 'var(--hf-accent)'
                            : 'var(--hf-ink-2)',
                        opacity: i === trend.length - 1 ? 1 : 0.6,
                      }}
                    />
                    <div
                      className='faint mono'
                      style={{ fontSize: 9, marginTop: 4 }}
                    >
                      {b.month ? b.month.slice(5) : ''}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Summary stats from platform */}
          {summary && (
            <div className='panel' style={{ padding: 18 }}>
              <div className='lbl'>
                {tr('console.billing.platform_summary', 'platform summary')}
              </div>
              <div
                style={{
                  marginTop: 10,
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 6,
                }}
              >
                {summary.subscription_plan && (
                  <div
                    style={{ display: 'flex', justifyContent: 'space-between' }}
                  >
                    <span className='muted'>
                      {tr('console.billing.plan', 'plan')}
                    </span>
                    <span className='mono strong'>
                      {summary.subscription_plan}
                    </span>
                  </div>
                )}
                {summary.wallet_balance_cny != null && (
                  <div
                    style={{ display: 'flex', justifyContent: 'space-between' }}
                  >
                    <span className='muted'>
                      {tr('console.billing.wallet', 'wallet')}
                    </span>
                    <span className='mono strong'>
                      {fmtCNY(summary.wallet_balance_cny)}
                    </span>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </HFShell>
  );
};

export default HFBilling;
