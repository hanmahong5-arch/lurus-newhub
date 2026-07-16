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
import { API, showError, showSuccess } from '../../../helpers';
import { useFormDraft } from '../../../hooks/common/useFormDraft';

// ─────────────────────────────────────────────────────────────────────────────
// Pure derivation helpers — exported for vitest coverage.

export const UNLIMITED_SENTINEL = -1;

/** True when max_balance signals "no ceiling". */
export const isUnlimited = (pool) =>
  !!pool && pool.max_balance === UNLIMITED_SENTINEL;

/**
 * Format the pool balance line for the drawer header.
 * Returns a human-readable string covering: no pool / unlimited / finite.
 */
export const formatBalanceLine = (pool) => {
  if (!pool) return 'No credit pool configured';
  if (isUnlimited(pool)) return `${pool.current_balance ?? 0} / ∞ (unlimited)`;
  return `${pool.current_balance ?? 0} / ${pool.max_balance}`;
};

/**
 * Pool health derived from current/max ratio. Values:
 *   "unlimited" — no ceiling
 *   "exhausted" — balance <= 0 (gate is now blocking traffic)
 *   "warning"   — balance < 20% of ceiling (matches CreditPoolBalanceLow intent)
 *   "ok"        — anything else
 */
export const poolHealth = (pool) => {
  if (!pool) return 'unconfigured';
  if (isUnlimited(pool)) return 'unlimited';
  if ((pool.current_balance ?? 0) <= 0) return 'exhausted';
  if (pool.current_balance / pool.max_balance < 0.2) return 'warning';
  return 'ok';
};

/** True when topup form should be disabled (no pool to topup into). */
export const isTopupDisabled = (pool) => !pool;

/** Validate a max_balance input — -1 OR >= 0 are valid. */
export const validateMaxBalance = (raw) => {
  const n = Number(raw);
  if (Number.isNaN(n)) return 'max_balance must be a number';
  if (n < 0 && n !== UNLIMITED_SENTINEL)
    return 'max_balance must be >= 0 or -1 (unlimited)';
  return null;
};

/** Validate a topup amount — must be a positive integer. */
export const validateTopupAmount = (raw) => {
  const n = Number(raw);
  if (!Number.isInteger(n)) return 'amount must be an integer';
  if (n <= 0) return 'amount must be > 0';
  return null;
};

// ─────────────────────────────────────────────────────────────────────────────
// Drawer component

const overlayStyle = {
  position: 'fixed',
  inset: 0,
  background: 'rgba(0,0,0,0.45)',
  zIndex: 500,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
};

const panelStyle = {
  background: 'var(--hf-paper)',
  border: '1px solid var(--hf-rule)',
  borderRadius: 4,
  padding: 28,
  width: 560,
  maxHeight: '90vh',
  overflowY: 'auto',
  display: 'flex',
  flexDirection: 'column',
  gap: 16,
};

const CreditPoolDrawer = ({ tenantId, tenantName, onClose }) => {
  const { t } = useTranslation();
  const [pool, setPool] = useState(null);
  const [draws, setDraws] = useState([]);
  const [loading, setLoading] = useState(true);

  // Both forms persist their in-progress state via useFormDraft (Tier 1.4)
  // so a tab close mid-fill recovers on reload. The storage key includes
  // tenantId so each tenant has its own draft slot — a Reseller flipping
  // between tenants doesn't see another tenant's half-typed topup amount.
  const [ceilingForm, setCeilingForm, clearCeilingDraft, , ceilingRestored] =
    useFormDraft(`credit-pool-ceiling:${tenantId}`, {
      max_balance: '',
      reset_period: 'monthly',
      alert_threshold_pct: 80,
    });
  const [topupForm, setTopupForm, clearTopupDraft, , topupRestored] =
    useFormDraft(`credit-pool-topup:${tenantId}`, { amount: '', reason: '' });
  const [submitting, setSubmitting] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get(
        `/api/v2/admin/tenants/${tenantId}/credit-pool`,
      );
      if (res?.data?.success) {
        setPool(res.data.data);
      }
    } catch (err) {
      if (err?.response?.status === 404) {
        setPool(null);
      } else {
        showError('Failed to load pool: ' + (err?.message || 'unknown'));
      }
    }
    try {
      const usage = await API.get(
        `/api/v2/admin/tenants/${tenantId}/credit-pool/usage?limit=20`,
      );
      if (usage?.data?.success) {
        setDraws(usage.data.data?.draws || []);
      }
    } catch (_) {
      setDraws([]);
    }
    setLoading(false);
  }, [tenantId]);

  useEffect(() => {
    reload();
  }, [reload]);

  const submitCeiling = async (e) => {
    e.preventDefault();
    const err = validateMaxBalance(ceilingForm.max_balance);
    if (err) {
      showError(err);
      return;
    }
    setSubmitting(true);
    try {
      const res = await API.post(
        `/api/v2/admin/tenants/${tenantId}/credit-pool`,
        {
          max_balance: Number(ceilingForm.max_balance),
          reset_period: ceilingForm.reset_period,
          alert_threshold_pct: Number(ceilingForm.alert_threshold_pct),
        },
      );
      if (res?.data?.success) {
        showSuccess('Credit pool created');
        clearCeilingDraft();
        await reload();
      } else {
        showError(res?.data?.message || 'Create failed');
      }
    } catch (err) {
      const code = err?.response?.data?.error_code;
      if (code === 'POOL_ALREADY_EXISTS') {
        showError('Credit pool already exists; use Topup instead');
      } else {
        showError(
          err?.response?.data?.message || err?.message || 'Create failed',
        );
      }
    } finally {
      setSubmitting(false);
    }
  };

  const submitTopup = async (e) => {
    e.preventDefault();
    const err = validateTopupAmount(topupForm.amount);
    if (err) {
      showError(err);
      return;
    }
    setSubmitting(true);
    try {
      const res = await API.post(
        `/api/v2/admin/tenants/${tenantId}/credit-pool/topup`,
        {
          amount: Number(topupForm.amount),
          reason: topupForm.reason || undefined,
        },
      );
      if (res?.data?.success) {
        showSuccess(
          `Topup successful. New balance: ${res.data.data.new_balance}`,
        );
        clearTopupDraft();
        await reload();
      } else {
        showError(res?.data?.message || 'Topup failed');
      }
    } catch (err) {
      const status = err?.response?.status;
      const code = err?.response?.data?.error_code;
      if (status === 402 || code === 'WALLET_DEBIT_FAILED') {
        showError('Wallet debit failed — insufficient platform balance');
      } else if (status === 409 || code === 'POOL_CEILING_EXCEEDED') {
        showError('Topup would exceed pool ceiling');
      } else {
        showError(
          err?.response?.data?.message || err?.message || 'Topup failed',
        );
      }
    } finally {
      setSubmitting(false);
    }
  };

  const health = poolHealth(pool);

  return (
    <div
      style={overlayStyle}
      onClick={(e) => e.target === e.currentTarget && onClose()}
      data-testid='credit-pool-overlay'
    >
      <div style={panelStyle}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <div className='strong' style={{ fontSize: 15 }}>
            {tenantName || tenantId} · credit pool
          </div>
          <button
            type='button'
            className='btn ghost sm'
            onClick={onClose}
            aria-label={t('console.common.close', 'close')}
          >
            ✕
          </button>
        </div>

        {loading && (
          <div className='muted' style={{ fontSize: 12 }}>
            Loading…
          </div>
        )}

        {!loading && (
          <>
            <div
              className='panel'
              style={{ padding: 14 }}
              data-testid='pool-summary'
            >
              <div className='lbl' style={{ marginBottom: 4 }}>
                Status
              </div>
              <div className='mono strong' style={{ fontSize: 13 }}>
                {formatBalanceLine(pool)} ·{' '}
                <span data-testid='pool-health'>{health}</span>
              </div>
              {pool?.next_reset_at && (
                <div className='muted' style={{ fontSize: 11, marginTop: 4 }}>
                  Next reset: {new Date(pool.next_reset_at).toLocaleString()}
                </div>
              )}
            </div>

            {!pool && (
              <form
                onSubmit={submitCeiling}
                style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
                data-testid='ceiling-form'
              >
                <div className='strong' style={{ fontSize: 13 }}>
                  Set ceiling
                </div>
                {ceilingRestored && (
                  <div
                    className='muted'
                    style={{
                      fontSize: 11,
                      padding: '4px 8px',
                      borderLeft: '2px solid var(--hf-warn)',
                      background: 'var(--hf-warn-bg)',
                    }}
                    data-testid='ceiling-restored-banner'
                  >
                    Restored from saved draft.{' '}
                    <button
                      type='button'
                      className='btn ghost xs'
                      onClick={clearCeilingDraft}
                      data-testid='ceiling-discard-draft'
                    >
                      Discard
                    </button>
                  </div>
                )}
                <label className='lbl'>
                  max_balance (use -1 for unlimited)
                </label>
                <input
                  type='number'
                  value={ceilingForm.max_balance}
                  onChange={(e) =>
                    setCeilingForm({
                      ...ceilingForm,
                      max_balance: e.target.value,
                    })
                  }
                  placeholder='1000000'
                  data-testid='ceiling-max-balance'
                />
                <label className='lbl'>reset_period</label>
                <select
                  value={ceilingForm.reset_period}
                  onChange={(e) =>
                    setCeilingForm({
                      ...ceilingForm,
                      reset_period: e.target.value,
                    })
                  }
                  data-testid='ceiling-reset-period'
                >
                  <option value='none'>none</option>
                  <option value='daily'>daily</option>
                  <option value='weekly'>weekly</option>
                  <option value='monthly'>monthly</option>
                </select>
                <label className='lbl'>alert_threshold_pct</label>
                <input
                  type='number'
                  min='1'
                  max='100'
                  value={ceilingForm.alert_threshold_pct}
                  onChange={(e) =>
                    setCeilingForm({
                      ...ceilingForm,
                      alert_threshold_pct: e.target.value,
                    })
                  }
                  data-testid='ceiling-alert-pct'
                />
                <button
                  type='submit'
                  className='btn primary'
                  disabled={submitting}
                  data-testid='ceiling-submit'
                >
                  {submitting ? '…' : 'Create pool'}
                </button>
              </form>
            )}

            <form
              onSubmit={submitTopup}
              style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
              data-testid='topup-form'
            >
              <div className='strong' style={{ fontSize: 13 }}>
                Topup from wallet
              </div>
              {topupRestored && (
                <div
                  className='muted'
                  style={{
                    fontSize: 11,
                    padding: '4px 8px',
                    borderLeft: '2px solid var(--hf-warn)',
                    background: 'var(--hf-warn-bg)',
                  }}
                  data-testid='topup-restored-banner'
                >
                  Restored from saved draft.{' '}
                  <button
                    type='button'
                    className='btn ghost xs'
                    onClick={clearTopupDraft}
                    data-testid='topup-discard-draft'
                  >
                    Discard
                  </button>
                </div>
              )}
              <label className='lbl'>amount (quota units)</label>
              <input
                type='number'
                value={topupForm.amount}
                onChange={(e) =>
                  setTopupForm({ ...topupForm, amount: e.target.value })
                }
                disabled={isTopupDisabled(pool)}
                data-testid='topup-amount'
              />
              <label className='lbl'>reason (optional)</label>
              <input
                type='text'
                value={topupForm.reason}
                onChange={(e) =>
                  setTopupForm({ ...topupForm, reason: e.target.value })
                }
                disabled={isTopupDisabled(pool)}
                data-testid='topup-reason'
              />
              <button
                type='submit'
                className='btn primary'
                disabled={submitting || isTopupDisabled(pool)}
                data-testid='topup-submit'
              >
                {submitting ? '…' : 'Topup'}
              </button>
            </form>

            <div data-testid='usage-section'>
              <div className='strong' style={{ fontSize: 13, marginBottom: 6 }}>
                Usage (recent)
              </div>
              {draws.length === 0 ? (
                <div className='muted' style={{ fontSize: 12 }}>
                  No draws yet.
                </div>
              ) : (
                <table
                  className='table sm'
                  style={{ width: '100%', fontSize: 12 }}
                >
                  <thead>
                    <tr>
                      <th>created</th>
                      <th>dir</th>
                      <th>amount</th>
                      <th>reason</th>
                    </tr>
                  </thead>
                  <tbody>
                    {draws.map((d) => (
                      <tr key={d.id}>
                        <td className='mono'>
                          {new Date(d.created_at).toLocaleString()}
                        </td>
                        <td>{d.direction === 1 ? 'debit' : 'credit'}</td>
                        <td className='mono'>{d.amount}</td>
                        <td>{d.reason}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
};

export default CreditPoolDrawer;
