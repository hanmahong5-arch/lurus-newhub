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
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import HfSkeletonRows from '../../../components/hifi/HfSkeletonRows';
import { API, showError, showSuccess } from '../../../helpers';
import CreditPoolDrawer from './CreditPoolDrawer';

/* HiFi 9 — Tenants admin. Wired to /api/v2/admin/tenants (2026-05-11). */

const QUOTA_PER_USD = 500_000;

const quotaToUSD = (q) => (q / QUOTA_PER_USD).toFixed(2);

/** Status 1=active, 2=disabled, 3=suspended */
const tenantStatusLabel = (s) => {
  if (s === 1) return 'active';
  if (s === 2) return 'disabled';
  if (s === 3) return 'suspended';
  return 'unknown';
};

const tenantStatusClass = (s) => {
  if (s === 1) return 'tag ok';
  if (s === 3) return 'tag warn';
  return 'tag';
};

const planTagClass = (plan) => {
  if (plan === 'enterprise') return 'tag acc';
  if (plan === 'free') return 'tag';
  return 'tag info';
};

// ─── Create tenant modal ──────────────────────────────────────────────────────

const PLANS = ['free', 'startup', 'team', 'enterprise'];

const CreateModal = ({ onCreated, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({
    name: '',
    slug: '',
    plan: 'free',
    quota_limit: '',
  });
  const [saving, setSaving] = useState(false);
  const nameRef = useRef(null);

  useEffect(() => {
    nameRef.current?.focus();
  }, []);

  const submit = async (e) => {
    e.preventDefault();
    if (!form.name.trim() || !form.slug.trim()) return;
    setSaving(true);
    try {
      const body = {
        name: form.name.trim(),
        slug: form.slug.trim(),
        plan: form.plan,
        quota_limit: form.quota_limit
          ? Math.round(parseFloat(form.quota_limit) * QUOTA_PER_USD)
          : 0,
        status: 1,
      };
      const res = await API.post('/api/v2/admin/tenants', body);
      if (res?.data?.success) {
        showSuccess(tr('console.tenant.toast_created', 'Tenant created'));
        onCreated();
      }
    } catch (_) {
      // error toast shown by API interceptor
    } finally {
      setSaving(false);
    }
  };

  const inputStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 12,
    padding: '5px 8px',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
    width: '100%',
  };

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.45)',
        zIndex: 500,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <form
        onSubmit={submit}
        style={{
          background: 'var(--hf-paper)',
          border: '1px solid var(--hf-rule)',
          borderRadius: 4,
          padding: 28,
          width: 420,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div className='strong' style={{ fontSize: 15 }}>
          {tr('console.tenant.modal_title', 'New tenant')}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.tenant.field_name', 'name *')}
          </span>
          <input
            ref={nameRef}
            style={inputStyle}
            placeholder={tr('console.tenant.ph_name', 'e.g. Acme Corp')}
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.tenant.field_slug', 'slug *')}
          </span>
          <input
            style={inputStyle}
            placeholder={tr('console.tenant.ph_slug', 'e.g. acme')}
            value={form.slug}
            onChange={(e) =>
              setForm((f) => ({
                ...f,
                slug: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'),
              }))
            }
            required
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>{tr('console.tenant.field_plan', 'plan')}</span>
          <select
            style={{ ...inputStyle, cursor: 'pointer' }}
            value={form.plan}
            onChange={(e) => setForm((f) => ({ ...f, plan: e.target.value }))}
          >
            {PLANS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr(
              'console.tenant.field_quota_limit',
              'quota limit ($, 0 = unlimited)',
            )}
          </span>
          <input
            style={inputStyle}
            type='number'
            min='0'
            step='0.01'
            placeholder='0'
            value={form.quota_limit}
            onChange={(e) =>
              setForm((f) => ({ ...f, quota_limit: e.target.value }))
            }
          />
        </label>

        <div
          style={{
            display: 'flex',
            gap: 8,
            justifyContent: 'flex-end',
            marginTop: 4,
          }}
        >
          <button type='button' className='btn ghost' onClick={onClose}>
            {tr('console.common.cancel', 'cancel')}
          </button>
          <button type='submit' className='btn primary' disabled={saving}>
            {saving
              ? tr('console.tenant.creating', 'creating…')
              : tr('console.tenant.create_tenant', 'create tenant')}
          </button>
        </div>
      </form>
    </div>
  );
};

// ─── Stats drawer ─────────────────────────────────────────────────────────────

const StatsDrawer = ({ tenant, onClose }) => {
  const { t: tr } = useTranslation();
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await API.get(`/api/v2/admin/tenants/${tenant.id}/stats`);
        if (!cancelled && res?.data?.success) {
          setStats(res.data.data);
        }
      } catch (_) {
        // silently handled
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tenant.id]);

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.45)',
        zIndex: 500,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        style={{
          background: 'var(--hf-paper)',
          border: '1px solid var(--hf-rule)',
          borderRadius: 4,
          padding: 28,
          width: 460,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <div className='strong' style={{ fontSize: 15 }}>
            {tenant.name} · {tr('console.tenant.stats_title', 'stats')}
          </div>
          <button
            type='button'
            className='btn ghost sm'
            onClick={onClose}
            aria-label={tr('console.common.close', 'close')}
          >
            ✕
          </button>
        </div>

        {loading && (
          <div className='muted' style={{ fontSize: 12 }}>
            {tr('console.common.loading', 'Loading…')}
          </div>
        )}

        {!loading && !stats && (
          <div className='muted' style={{ fontSize: 12 }}>
            {tr('console.tenant.no_stats', 'No stats available.')}
          </div>
        )}

        {!loading && stats && (
          <div className='panel'>
            {Object.entries(stats).map(([k, v], i, arr) => (
              <div
                key={k}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '160px 1fr',
                  padding: '10px 16px',
                  borderBottom:
                    i < arr.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                  fontSize: 12,
                  alignItems: 'center',
                }}
              >
                <span className='lbl'>{k}</span>
                <span className='mono strong'>{String(v)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

// ─── Rate-limits modal ────────────────────────────────────────────────────────
// Edits tenant-level RPM/TPM caps. JSON keys mirror entity/tenant.go tags
// (rate_limit_rpm / rate_limit_tpm); 0 = unlimited.

const LimitsModal = ({ tenant, onSaved, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({
    rpm: tenant.rate_limit_rpm > 0 ? String(tenant.rate_limit_rpm) : '',
    tpm: tenant.rate_limit_tpm > 0 ? String(tenant.rate_limit_tpm) : '',
  });
  const [saving, setSaving] = useState(false);

  const inputStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 12,
    padding: '5px 8px',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
    width: '100%',
  };

  const submit = async (e) => {
    e.preventDefault();
    setSaving(true);
    try {
      const res = await API.put(`/api/v2/admin/tenants/${tenant.id}`, {
        rate_limit_rpm: Math.max(0, parseInt(form.rpm, 10) || 0),
        rate_limit_tpm: Math.max(0, parseInt(form.tpm, 10) || 0),
      });
      if (res?.data?.success) {
        showSuccess(
          tr('console.tenant.toast_limits_saved', 'Rate limits saved'),
        );
        onSaved();
      }
    } catch (_) {
      // error toast shown by API interceptor
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.45)',
        zIndex: 500,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <form
        onSubmit={submit}
        style={{
          background: 'var(--hf-paper)',
          border: '1px solid var(--hf-rule)',
          borderRadius: 4,
          padding: 28,
          width: 420,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div className='strong' style={{ fontSize: 15 }}>
          {tenant.name} · {tr('console.tenant.limits_title', 'rate limits')}
        </div>

        <div className='muted' style={{ fontSize: 11 }}>
          {tr(
            'console.tenant.limits_hint',
            'Aggregate caps across all tokens under this tenant. 0 = unlimited.',
          )}
        </div>

        <div
          style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}
        >
          {[
            ['rpm', tr('console.tenant.field_rpm', 'rpm limit')],
            ['tpm', tr('console.tenant.field_tpm', 'tpm limit')],
          ].map(([k, label]) => (
            <label
              key={k}
              style={{ display: 'flex', flexDirection: 'column', gap: 5 }}
            >
              <span className='lbl'>{label}</span>
              <input
                style={inputStyle}
                type='number'
                min='0'
                step='1'
                placeholder={tr(
                  'console.tenant.ph_rate_limit',
                  '0 = unlimited',
                )}
                value={form[k]}
                onChange={(e) =>
                  setForm((f) => ({ ...f, [k]: e.target.value }))
                }
              />
            </label>
          ))}
        </div>

        <div
          style={{
            display: 'flex',
            gap: 8,
            justifyContent: 'flex-end',
            marginTop: 4,
          }}
        >
          <button type='button' className='btn ghost' onClick={onClose}>
            {tr('console.common.cancel', 'cancel')}
          </button>
          <button type='submit' className='btn primary' disabled={saving}>
            {saving
              ? tr('console.common.loading', 'loading…')
              : tr('console.common.save', 'save')}
          </button>
        </div>
      </form>
    </div>
  );
};

// ─── Main page ────────────────────────────────────────────────────────────────

const HFTenants = () => {
  // Aliased to `tr`: `t` is used as the tenant loop variable in .map/.reduce
  // callbacks below and would shadow the translator.
  const { t: tr } = useTranslation();
  const [tenants, setTenants] = useState([]);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [creating, setCreating] = useState(false);
  const [statsTarget, setStatsTarget] = useState(null); // tenant object for stats drawer
  const [poolTarget, setPoolTarget] = useState(null); // tenant object for credit-pool drawer
  const [limitsTarget, setLimitsTarget] = useState(null); // tenant object for rate-limits modal
  const [actioning, setActioning] = useState(null); // tenant id being actioned
  // Tier 1.3: typed-confirmation for enable / disable / suspend. The
  // pending action is { tenant, action } when armed; null when closed.
  const [actionConfirm, setActionConfirm] = useState(null);

  const searchRef = useRef(null);

  const fetchTenants = useCallback(async (kw = '') => {
    setLoading(true);
    setForbidden(false);
    try {
      const params = new URLSearchParams({ page: 1, page_size: 50 });
      if (kw.trim()) params.set('keyword', kw.trim());
      const res = await API.get(`/api/v2/admin/tenants?${params}`);
      if (res?.data?.success) {
        setTenants(res.data.data.tenants ?? res.data.data.items ?? []);
      }
    } catch (err) {
      if (err?.response?.status === 403) {
        setForbidden(true);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchTenants();
  }, [fetchTenants]);

  // Debounced keyword search
  useEffect(() => {
    const t = setTimeout(() => fetchTenants(keyword), 350);
    return () => clearTimeout(t);
  }, [keyword, fetchTenants]);

  const handleAction = (tenant, action) => {
    setActionConfirm({ tenant, action });
  };

  const performAction = async () => {
    if (!actionConfirm) return;
    const { tenant, action } = actionConfirm;
    const toastByAction = {
      enable: tr('console.tenant.toast_enabled', 'Tenant enabled'),
      disable: tr('console.tenant.toast_disabled', 'Tenant disabled'),
      suspend: tr('console.tenant.toast_suspended', 'Tenant suspended'),
    };
    setActioning(tenant.id);
    try {
      const res = await API.post(
        `/api/v2/admin/tenants/${tenant.id}/${action}`,
        {},
      );
      if (res?.data?.success) {
        showSuccess(toastByAction[action]);
        setActionConfirm(null);
        await fetchTenants(keyword);
      }
    } catch (_) {
      // error toast from interceptor
    } finally {
      setActioning(null);
    }
  };

  const handleCreated = async () => {
    setCreating(false);
    await fetchTenants(keyword);
  };

  const totalUsedUSD = tenants
    .reduce((s, t) => s + (t.used_quota || 0) / QUOTA_PER_USD, 0)
    .toFixed(2);

  return (
    <HFShell
      active='users'
      crumbs={[
        tr('console.nav.section_platform_admin', 'platform · admin'),
        tr('console.tenant.crumb', 'tenants'),
      ]}
      actions={
        <>
          {loading ? (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.common.loading', 'loading…')}
            </span>
          ) : (
            !forbidden && (
              <span className='muted mono' style={{ fontSize: 11 }}>
                {tr('console.tenant.count', '{{count}} tenants', {
                  count: tenants.length,
                })}{' '}
                ·{' '}
                {tr('console.tenant.used_amount', '${{amount}} used', {
                  amount: totalUsedUSD,
                })}
              </span>
            )
          )}
          <button
            type='button'
            className='btn primary'
            onClick={() => setCreating(true)}
          >
            {tr('console.tenant.new_tenant', '+ new tenant')}
          </button>
        </>
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.tenant.crumb', 'tenants')}
          </div>
          <h1>
            {loading
              ? '…'
              : forbidden
                ? tr('console.tenant.admin_required', 'Admin access required')
                : tr('console.tenant.count', '{{count}} tenants', {
                    count: tenants.length,
                  })}
            {!loading && !forbidden && parseFloat(totalUsedUSD) > 0 && (
              <span className='muted' style={{ fontWeight: 400 }}>
                {' '}
                ·{' '}
                {tr('console.tenant.used_amount', '${{amount}} used', {
                  amount: totalUsedUSD,
                })}
              </span>
            )}
          </h1>
          <div className='sub'>
            {tr(
              'console.tenant.sub',
              'isolation · per-tenant keys · per-tenant budgets',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr('console.tenant.admin_required', 'Admin access required')}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.tenant.admin_required_body',
                'You do not have permission to manage tenants. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          {/* Search bar */}
          <div style={{ marginBottom: 16 }}>
            <input
              ref={searchRef}
              style={{
                fontFamily: 'var(--hf-mono)',
                fontSize: 12,
                padding: '6px 10px',
                border: '1px solid var(--hf-rule)',
                background: 'var(--hf-sunken)',
                color: 'var(--hf-ink)',
                borderRadius: 2,
                outline: 'none',
                width: 260,
              }}
              placeholder={tr('console.tenant.ph_search', 'search tenants…')}
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
          </div>

          <div className='panel'>
            {loading ? (
              <div style={{ padding: '10px 24px' }}>
                <HfSkeletonRows rows={5} />
              </div>
            ) : tenants.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {keyword
                  ? tr(
                      'console.tenant.empty_search',
                      'No tenants match your search.',
                    )
                  : tr(
                      'console.tenant.empty',
                      'No tenants yet. Create one to get started.',
                    )}
              </div>
            ) : (
              <div className='hf-table-scroll'>
                <table className='t'>
                  <thead>
                    <tr>
                      <th>{tr('console.tenant.th_tenant', 'tenant')}</th>
                      <th>{tr('console.tenant.th_plan', 'plan')}</th>
                      <th>{tr('console.tenant.th_status', 'status')}</th>
                      <th>{tr('console.tenant.th_users', 'users')}</th>
                      <th>{tr('console.tenant.th_used', 'used')}</th>
                      <th>{tr('console.tenant.th_quota_cap', 'quota cap')}</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {tenants.map((t) => {
                      const usedUSD = parseFloat(quotaToUSD(t.used_quota || 0));
                      const capUSD =
                        t.max_quota > 0
                          ? parseFloat(quotaToUSD(t.max_quota))
                          : null;
                      const pct = capUSD ? Math.min(usedUSD / capUSD, 1) : 0;
                      const isActioning = actioning === t.id;

                      return (
                        <tr key={t.id} data-testid={`tenant-row-${t.id}`}>
                          <td>
                            <div
                              style={{
                                display: 'flex',
                                alignItems: 'center',
                                gap: 10,
                              }}
                            >
                              <div
                                style={{
                                  width: 28,
                                  height: 28,
                                  background: 'var(--hf-sunken)',
                                  display: 'flex',
                                  alignItems: 'center',
                                  justifyContent: 'center',
                                  fontFamily: 'var(--hf-display)',
                                  fontWeight: 600,
                                  color: 'var(--hf-ink)',
                                  flexShrink: 0,
                                }}
                              >
                                {(t.name || t.slug || '?')[0].toUpperCase()}
                              </div>
                              <div>
                                <div className='strong'>{t.name}</div>
                                <div
                                  className='faint mono'
                                  style={{ fontSize: 10 }}
                                >
                                  {t.slug}
                                </div>
                              </div>
                            </div>
                          </td>

                          <td>
                            <span className={planTagClass(t.plan_type)}>
                              {t.plan_type || '—'}
                            </span>
                          </td>

                          <td>
                            <span
                              data-testid={`tenant-status-${t.id}`}
                              className={tenantStatusClass(t.status)}
                            >
                              {tr(
                                `console.tenant.status_${tenantStatusLabel(t.status)}`,
                                tenantStatusLabel(t.status),
                              )}
                            </span>
                          </td>

                          <td className='mono'>{t.user_count ?? '—'}</td>

                          <td>
                            <span className='display' style={{ fontSize: 15 }}>
                              ${quotaToUSD(t.used_quota || 0)}
                            </span>
                          </td>

                          <td>
                            {capUSD ? (
                              <div
                                style={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: 8,
                                }}
                              >
                                <div
                                  style={{
                                    width: 80,
                                    height: 4,
                                    background: 'var(--hf-sunken)',
                                    flexShrink: 0,
                                  }}
                                >
                                  <div
                                    style={{
                                      width: `${pct * 100}%`,
                                      height: '100%',
                                      background:
                                        pct > 0.9
                                          ? 'var(--hf-err)'
                                          : pct > 0.75
                                            ? 'var(--hf-warn)'
                                            : 'var(--hf-ok)',
                                    }}
                                  />
                                </div>
                                <span
                                  className='mono muted'
                                  style={{ fontSize: 10 }}
                                >
                                  ${quotaToUSD(t.max_quota)}
                                </span>
                              </div>
                            ) : (
                              <span
                                className='mono muted'
                                style={{ fontSize: 11 }}
                              >
                                ∞
                              </span>
                            )}
                          </td>

                          <td>
                            <div
                              style={{
                                display: 'flex',
                                gap: 6,
                                alignItems: 'center',
                              }}
                            >
                              <button
                                type='button'
                                className='btn ghost sm'
                                data-testid={`tenant-stats-btn-${t.id}`}
                                disabled={isActioning}
                                onClick={() => setStatsTarget(t)}
                              >
                                {tr('console.tenant.btn_stats', 'stats')}
                              </button>
                              <button
                                type='button'
                                className='btn ghost sm'
                                disabled={isActioning}
                                onClick={() => setPoolTarget(t)}
                              >
                                {tr('console.tenant.btn_pool', 'pool')}
                              </button>
                              <button
                                type='button'
                                className='btn ghost sm'
                                data-testid={`tenant-limits-btn-${t.id}`}
                                disabled={isActioning}
                                onClick={() => setLimitsTarget(t)}
                              >
                                {tr('console.tenant.btn_limits', 'limits')}
                              </button>
                              {t.status !== 1 && (
                                <button
                                  type='button'
                                  className='btn ghost sm'
                                  data-testid={`tenant-enable-btn-${t.id}`}
                                  disabled={isActioning}
                                  onClick={() => handleAction(t, 'enable')}
                                >
                                  {isActioning
                                    ? '…'
                                    : tr('console.tenant.btn_enable', 'enable')}
                                </button>
                              )}
                              {t.status === 1 && (
                                <button
                                  type='button'
                                  className='btn ghost sm'
                                  data-testid={`tenant-disable-btn-${t.id}`}
                                  disabled={isActioning}
                                  onClick={() => handleAction(t, 'disable')}
                                >
                                  {isActioning
                                    ? '…'
                                    : tr(
                                        'console.tenant.btn_disable',
                                        'disable',
                                      )}
                                </button>
                              )}
                              {t.status !== 3 && (
                                <button
                                  type='button'
                                  className='btn ghost sm'
                                  data-testid={`tenant-suspend-btn-${t.id}`}
                                  disabled={isActioning}
                                  style={{ color: 'var(--hf-warn)' }}
                                  onClick={() => handleAction(t, 'suspend')}
                                >
                                  {isActioning
                                    ? '…'
                                    : tr(
                                        'console.tenant.btn_suspend',
                                        'suspend',
                                      )}
                                </button>
                              )}
                            </div>
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

      {creating && (
        <CreateModal
          onCreated={handleCreated}
          onClose={() => setCreating(false)}
        />
      )}

      {statsTarget && (
        <StatsDrawer
          tenant={statsTarget}
          onClose={() => setStatsTarget(null)}
        />
      )}

      {poolTarget && (
        <CreditPoolDrawer
          tenantId={poolTarget.id}
          tenantName={poolTarget.name}
          onClose={() => setPoolTarget(null)}
        />
      )}

      {limitsTarget && (
        <LimitsModal
          tenant={limitsTarget}
          onSaved={async () => {
            setLimitsTarget(null);
            await fetchTenants(keyword);
          }}
          onClose={() => setLimitsTarget(null)}
        />
      )}

      <ConfirmDialog
        visible={!!actionConfirm}
        title={tr(
          'console.tenant.confirm_action_title',
          'Perform {{action}} on tenant "{{name}}"?',
          {
            name: actionConfirm?.tenant?.name || '',
            action: actionConfirm?.action || '',
          },
        )}
        consequenceList={[
          tr(
            'console.tenant.confirm_action_consequence',
            'This affects every token and channel under this tenant',
          ),
        ]}
        confirmText={actionConfirm?.tenant?.name || ''}
        confirmButtonType={
          actionConfirm?.action === 'enable' ? 'warning' : 'danger'
        }
        onConfirm={performAction}
        onCancel={() =>
          !(actioning && actioning === actionConfirm?.tenant?.id) &&
          setActionConfirm(null)
        }
      />
    </HFShell>
  );
};

export default HFTenants;
