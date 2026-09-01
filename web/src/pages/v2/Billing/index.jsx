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

// Client-side amount presets + bounds. These are a UX guard only — the
// platform remains the authority and enforces the same bounds server-side
// (lurus-platform wallet.go minTopupCNY=1 / maxTopupCNY=100000) with a 400 on
// violation, so a stale/looser client constant here can never let an invalid
// amount through, only produce a late 400 instead of an early inline error.
const AMOUNT_PRESETS_CNY = [100, 500, 1000, 5000];
const MIN_TOPUP_CNY = 1;
const MAX_TOPUP_CNY = 100000;

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

  // Server-driven payment methods. `methodsLoaded` distinguishes "still
  // fetching" from "fetched, platform has none configured" — only the latter
  // is the honest empty state (fetch failure also lands here, fail-safe: we
  // never assume alipay/any method is available).
  const [methods, setMethods] = useState([]);
  const [methodsLoaded, setMethodsLoaded] = useState(false);
  const [selectedMethod, setSelectedMethod] = useState('');
  const [amount, setAmount] = useState(String(AMOUNT_PRESETS_CNY[0]));

  const fetchAll = useCallback(async () => {
    if (!tenantSlug) return;
    setLoading(true);
    setPlatformDown(false);
    try {
      // allSettled so a platform-summary/payment-methods failure doesn't also
      // blank the invoices table. summary + payment-methods use
      // skipErrorHandler so their failure shows the banner / honest empty
      // state, not a duplicate toast.
      const [invRes, sumRes, methodsRes] = await Promise.allSettled([
        API.get(`/api/v2/${tenantSlug}/billing/invoices`),
        API.get('/api/v2/user/billing/summary', { skipErrorHandler: true }),
        API.get('/api/v2/user/billing/payment-methods', {
          skipErrorHandler: true,
        }),
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

      // Fetch failure or a non-array/empty payload both resolve to "no
      // payment method configured" — the honest empty state, never a silent
      // fallback to a hardcoded provider.
      const rawList =
        methodsRes.status === 'fulfilled' && methodsRes.value?.data?.success
          ? methodsRes.value.data.data
          : [];
      const list = Array.isArray(rawList) ? rawList : [];
      setMethods(list);
      setMethodsLoaded(true);
      setSelectedMethod((prev) =>
        prev && list.some((m) => m.id === prev) ? prev : (list[0]?.id ?? ''),
      );
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

  // Client-side bounds check only — a UX guard, not the authority. Returns
  // the parsed amount or null when out of bounds / not a number.
  const parsedAmount = (() => {
    const n = Number(amount);
    if (!Number.isFinite(n) || n < MIN_TOPUP_CNY || n > MAX_TOPUP_CNY) {
      return null;
    }
    return n;
  })();
  const amountInvalid = parsedAmount === null;
  const noPaymentMethods = methodsLoaded && methods.length === 0;

  const handleRecharge = async () => {
    if (noPaymentMethods || !selectedMethod) {
      showError(
        tr(
          'console.billing.no_payment_methods',
          'No payment method is configured yet — top-up is temporarily unavailable',
        ),
      );
      return;
    }
    if (amountInvalid) {
      showError(
        tr(
          'console.billing.amount_invalid',
          'enter an amount between {{min}} and {{max}} CNY',
          {
            min: MIN_TOPUP_CNY,
            max: MAX_TOPUP_CNY,
          },
        ),
      );
      return;
    }
    setRecharging(true);
    try {
      const res = await API.post(
        '/api/v2/user/billing/checkout',
        {
          amount_cny: parsedAmount,
          payment_method: selectedMethod,
          return_url: window.location.href,
        },
        { skipErrorHandler: true },
      );
      if (res?.data?.success && res.data.data?.pay_url) {
        window.location.href = res.data.data.pay_url;
      } else {
        showError(
          res?.data?.message ||
            tr(
              'console.billing.checkout_failed',
              'Failed to create top-up order',
            ),
        );
      }
    } catch (err) {
      // A platform 400 (bad amount / method not configured) is a rejected
      // request, not an outage — surface its real message and leave the
      // platform-down banner alone. Anything else (network error, 5xx, or no
      // response at all) is the same outage class as the summary fetch.
      if (err?.response?.status === 400) {
        showError(
          err?.response?.data?.message ||
            tr(
              'console.billing.method_not_available',
              'top-up unavailable: no payment method configured',
            ),
        );
      } else {
        setPlatformDown(true);
        showError(
          tr(
            'console.billing.service_unavailable',
            'Billing service temporarily unavailable — please try again later',
          ),
        );
      }
    } finally {
      setRecharging(false);
    }
  };

  // Trend: last 6 invoices in chronological order for the bar chart.
  const trend = invoices.slice(0, 6).slice().reverse();
  const trendMax = trend.reduce((m, b) => Math.max(m, b.amount_cny ?? 0), 1);

  // The platform BillingSummary DTO emits `balance`; `wallet_balance_cny` was
  // never one of its fields, so reading only that showed an em dash forever.
  // The old key is kept as a fallback in case some other producer sends it.
  const walletBalance = summary?.balance ?? summary?.wallet_balance_cny;
  const balanceDisplay = walletBalance != null ? fmtCNY(walletBalance) : '—';
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
            disabled={recharging || noPaymentMethods}
            title={
              noPaymentMethods
                ? tr(
                    'console.billing.no_payment_methods',
                    'No payment method is configured yet — top-up is temporarily unavailable',
                  )
                : undefined
            }
            data-testid='billing-recharge'
          >
            {recharging
              ? tr('console.billing.processing', 'processing…')
              : noPaymentMethods
                ? tr(
                    'console.billing.method_not_available',
                    'top-up unavailable: no payment method configured',
                  )
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
          {/* Top-up amount — server bounds are authoritative; presets +
              custom input are a client-side UX guard only. */}
          <div className='panel' style={{ padding: 18 }}>
            <div className='lbl'>
              {tr('console.billing.amount_label', 'top-up amount (CNY)')}
            </div>
            <div
              style={{
                marginTop: 10,
                display: 'flex',
                gap: 6,
                flexWrap: 'wrap',
              }}
            >
              {AMOUNT_PRESETS_CNY.map((preset) => (
                <button
                  key={preset}
                  type='button'
                  className={
                    amount === String(preset)
                      ? 'btn sm primary'
                      : 'btn sm ghost'
                  }
                  onClick={() => setAmount(String(preset))}
                  data-testid={`billing-amount-preset-${preset}`}
                >
                  ¥{preset}
                </button>
              ))}
            </div>
            <label
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: 5,
                marginTop: 10,
              }}
            >
              <span className='faint mono' style={{ fontSize: 10 }}>
                {tr('console.billing.amount_custom', 'custom amount')}
              </span>
              <input
                type='number'
                min={MIN_TOPUP_CNY}
                max={MAX_TOPUP_CNY}
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                style={{
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 12,
                  padding: '5px 8px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                  outline: 'none',
                  width: '100%',
                }}
                data-testid='billing-amount-custom'
              />
            </label>
            {amountInvalid && (
              <div
                className='faint'
                style={{ marginTop: 6, color: 'var(--hf-err)', fontSize: 10 }}
                data-testid='billing-amount-error'
              >
                {tr(
                  'console.billing.amount_invalid',
                  'enter an amount between {{min}} and {{max}} CNY',
                  { min: MIN_TOPUP_CNY, max: MAX_TOPUP_CNY },
                )}
              </div>
            )}
          </div>

          {/* Payment method — server-driven (GET .../billing/payment-methods).
              An empty, successfully-loaded list is the honest "not yet
              configured" state, not a hardcoded fallback provider. */}
          <div className='panel' style={{ padding: 18 }}>
            <div className='lbl'>
              {tr('console.billing.payment_method', 'payment method')}
            </div>
            {noPaymentMethods ? (
              <div
                className='panel-paper'
                role='status'
                data-testid='billing-no-payment-methods'
                style={{
                  marginTop: 10,
                  padding: 14,
                  color: 'var(--hf-ink-2)',
                  fontSize: 11,
                  lineHeight: 1.5,
                }}
              >
                {tr(
                  'console.billing.no_payment_methods',
                  'No payment method is configured yet — top-up is temporarily unavailable',
                )}
              </div>
            ) : (
              <div
                role='radiogroup'
                aria-label={tr(
                  'console.billing.method_label',
                  'select payment method',
                )}
                style={{
                  marginTop: 10,
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 8,
                }}
              >
                {methods.map((m) => (
                  <label
                    key={m.id}
                    className='panel-paper'
                    style={{
                      padding: 10,
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                      cursor: 'pointer',
                      border:
                        selectedMethod === m.id
                          ? '1px solid var(--hf-accent)'
                          : '1px solid var(--hf-rule)',
                    }}
                  >
                    <input
                      type='radio'
                      name='billing-payment-method'
                      checked={selectedMethod === m.id}
                      onChange={() => setSelectedMethod(m.id)}
                      data-testid={`billing-method-${m.id}`}
                    />
                    <div>
                      <div className='mono strong'>{m.name}</div>
                      <div className='faint mono' style={{ fontSize: 10 }}>
                        {m.provider}
                      </div>
                    </div>
                  </label>
                ))}
              </div>
            )}
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
                {walletBalance != null && (
                  <div
                    style={{ display: 'flex', justifyContent: 'space-between' }}
                  >
                    <span className='muted'>
                      {tr('console.billing.wallet', 'wallet')}
                    </span>
                    <span className='mono strong'>{fmtCNY(walletBalance)}</span>
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
