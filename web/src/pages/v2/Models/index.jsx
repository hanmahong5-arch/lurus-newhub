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
import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import WIPBanner from '../../../components/hifi/WIPBanner';
import { API, showError, showSuccess } from '../../../helpers';
import { useFormDraft } from '../../../hooks/common/useFormDraft';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/* HiFi 7 — Models catalog. Wired to GET /api/v2/:tenant_slug/models (2026-05-19).
   Wave 3 Phase 1 (2026-05-20): add-model modal + try ↗ navigate wired. */

// Known vendor names for the add-model select. The list is used for UX
// convenience only — the backend accepts any vendor string.
const KNOWN_VENDORS = [
  'OpenAI',
  'Anthropic',
  'Google',
  'Baidu',
  'Tencent',
  'Zhipu',
  'Minimax',
  'Cohere',
  'Perplexity',
  'Siliconflow',
  'AWS',
  'Other',
];

// Labels resolved at render via tr() — module scope has no i18n context.
const QUOTA_TYPE_OPTIONS = [
  { value: 0, key: 'quota_type_token', fallback: 'pay-as-you-go (token)' },
  { value: 1, key: 'quota_type_times', fallback: 'pay-per-call' },
];

const DRAFT_KEY = 'models-add-form';
const DRAFT_INITIAL = {
  model_name: '',
  vendor: '',
  model_ratio: 1,
  quota_type: 0,
  model_price: '',
};

const STATUS_LABEL = { 1: 'active', 0: 'disabled' };

const HFModels = () => {
  const tenantSlug = useTenantSlug();
  const navigate = useNavigate();
  // Aliased to `tr` per the v2 console convention.
  const { t: tr } = useTranslation();

  const [vendor, setVendor] = useState('');
  const [models, setModels] = useState([]);
  const [total, setTotal] = useState(0);
  const [vendorCounts, setVendorCounts] = useState({});
  const [loading, setLoading] = useState(false);

  // Add-model modal state.
  const [addOpen, setAddOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [form, setForm, clearDraft] = useFormDraft(DRAFT_KEY, DRAFT_INITIAL);
  const dialogRef = useRef(null);

  // Open / close modal — sync <dialog> element with state.
  // Guard for jsdom / older browsers that don't implement showModal().
  useEffect(() => {
    const el = dialogRef.current;
    if (!el) return;
    if (addOpen) {
      if (!el.open && typeof el.showModal === 'function') el.showModal();
    } else {
      if (el.open && typeof el.close === 'function') el.close();
    }
  }, [addOpen]);

  const fetchModels = async (slug, vendorFilter) => {
    setLoading(true);
    try {
      const params = new URLSearchParams({ limit: '100', offset: '0' });
      if (vendorFilter) params.set('vendor', vendorFilter);
      const res = await API.get(`/api/v2/${slug}/models?${params.toString()}`);
      const d = res?.data?.data ?? {};
      setModels(d.items ?? []);
      setTotal(d.total ?? 0);
      if (d.vendor_counts) setVendorCounts(d.vendor_counts);
    } catch (err) {
      const msg =
        err?.response?.data?.message ??
        err?.message ??
        tr('console.models.load_failed', 'Failed to load models');
      showError(msg);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      setLoading(true);
      try {
        const params = new URLSearchParams({ limit: '100', offset: '0' });
        if (vendor) params.set('vendor', vendor);
        const res = await API.get(
          `/api/v2/${tenantSlug}/models?${params.toString()}`,
        );
        if (cancelled) return;
        const d = res?.data?.data ?? {};
        setModels(d.items ?? []);
        setTotal(d.total ?? 0);
        if (d.vendor_counts) setVendorCounts(d.vendor_counts);
      } catch (err) {
        if (cancelled) return;
        const msg =
          err?.response?.data?.message ??
          err?.message ??
          tr('console.models.load_failed', 'Failed to load models');
        showError(msg);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    run();
    return () => {
      cancelled = true;
    };
  }, [tenantSlug, vendor]);

  // Build vendor filter pills from vendor_counts; add "all" pseudo-entry.
  const vendorNames = Object.keys(vendorCounts).filter(Boolean).sort();

  // ── Add-model submit ──────────────────────────────────────────────────────
  const handleAddSubmit = async (e) => {
    e.preventDefault();
    if (!form.model_name.trim()) {
      showError(
        tr('console.models.name_required', 'Model name cannot be empty'),
      );
      return;
    }
    const perCall = Number(form.quota_type) === 1;
    const modelPrice = Number(form.model_price);
    // 按次计费必须带单价：后端会拒绝无价的按次模型，避免它静默按默认价计费。
    if (perCall && !(modelPrice > 0)) {
      showError(
        tr(
          'console.models.price_required',
          'Price per call is required for pay-per-call billing',
        ),
      );
      return;
    }
    setAdding(true);
    try {
      await API.post(`/api/v2/${tenantSlug}/models`, {
        model_name: form.model_name.trim(),
        vendor: form.vendor,
        model_ratio: Number(form.model_ratio) || 1,
        quota_type: Number(form.quota_type),
        ...(perCall ? { model_price: modelPrice } : {}),
      });
      showSuccess(tr('console.models.toast_added', 'Model added'));
      clearDraft();
      setAddOpen(false);
      // Refresh list.
      await fetchModels(tenantSlug, vendor);
    } catch (err) {
      const msg =
        err?.response?.data?.message ??
        err?.message ??
        tr('console.models.add_failed', 'Failed to add model');
      showError(msg);
    } finally {
      setAdding(false);
    }
  };

  return (
    <>
      <HFShell
        active='models'
        crumbs={[
          tr('console.nav.section_routing_models', 'routing & models'),
          tr('console.models.crumb', 'model management'),
        ]}
        actions={
          <>
            {/* single-model editing deferred to v3 */}
            <WIPBanner
              reason={tr(
                'console.models.wip_single_edit',
                'single-model editing deferred to v3',
              )}
              todo='v3 story: per-model enable/disable'
            />
            <button
              type='button'
              className='btn primary'
              data-testid='models-add-btn'
              onClick={() => setAddOpen(true)}
            >
              {tr('console.models.add_btn', '+ add model')}
            </button>
          </>
        }
      >
        <div className='hf-page-head'>
          <div>
            <div className='lbl' style={{ marginBottom: 6 }}>
              {tr('console.models.catalog', 'catalog')}
            </div>
            <h1>
              {loading ? '…' : total}{' '}
              <span className='muted' style={{ fontWeight: 400 }}>
                {tr('console.models.unit_models', 'models')}
              </span>
            </h1>
          </div>
        </div>

        {/* Vendor filter pills */}
        <div
          style={{
            display: 'flex',
            gap: 10,
            padding: '14px 28px',
            borderBottom: '1px solid var(--hf-rule)',
            background: 'var(--hf-paper)',
            alignItems: 'center',
            flexWrap: 'wrap',
          }}
        >
          <span className='lbl'>{tr('console.models.vendor', 'vendor')}</span>
          <button
            key='all'
            type='button'
            data-testid='vendor-filter-all'
            onClick={() => setVendor('')}
            className={'pill ' + (!vendor ? 'solid' : '')}
            style={{
              cursor: 'pointer',
              border: '1px solid var(--hf-rule)',
              background: !vendor ? 'var(--hf-ink)' : 'var(--hf-elev)',
              color: !vendor ? 'var(--hf-bg)' : 'var(--hf-ink-2)',
            }}
          >
            {tr('console.models.all', 'all')} ({total})
          </button>
          {vendorNames.map((v) => (
            <button
              key={v}
              type='button'
              data-testid={`vendor-filter-${v}`}
              onClick={() => setVendor(v)}
              className={'pill ' + (vendor === v ? 'solid' : '')}
              style={{
                cursor: 'pointer',
                border: '1px solid var(--hf-rule)',
                background: vendor === v ? 'var(--hf-ink)' : 'var(--hf-elev)',
                color: vendor === v ? 'var(--hf-bg)' : 'var(--hf-ink-2)',
              }}
            >
              {v} ({vendorCounts[v] ?? 0})
            </button>
          ))}
        </div>

        {/* Model grid */}
        {loading ? (
          <div
            data-testid='models-loading'
            style={{
              padding: 48,
              textAlign: 'center',
              color: 'var(--hf-ink-2)',
            }}
          >
            {tr('console.common.loading', 'loading…')}
          </div>
        ) : (
          <div
            style={{
              padding: 24,
              display: 'grid',
              gridTemplateColumns: 'repeat(3, 1fr)',
              gap: 14,
            }}
          >
            {models.map((m) => (
              <div
                key={m.id}
                className='panel'
                data-testid={`model-card-${m.model_name}`}
                style={{
                  padding: 18,
                  position: 'relative',
                  overflow: 'hidden',
                }}
              >
                <div className='lbl' style={{ color: 'var(--hf-ink-2)' }}>
                  {m.vendor ||
                    tr('console.models.unknown_vendor', 'unknown vendor')}
                </div>
                <div
                  className='display'
                  style={{
                    fontSize: 20,
                    marginTop: 6,
                    letterSpacing: '-0.025em',
                  }}
                >
                  {m.model_name}
                </div>
                <div
                  className='muted mono'
                  style={{ fontSize: 11, marginTop: 4 }}
                >
                  {tr('console.models.status', 'status')}:{' '}
                  {STATUS_LABEL[m.status]
                    ? tr(
                        `console.models.status_${STATUS_LABEL[m.status]}`,
                        STATUS_LABEL[m.status],
                      )
                    : m.status}
                </div>

                <div style={{ display: 'flex', gap: 6, marginTop: 14 }}>
                  <button
                    type='button'
                    className='btn sm'
                    data-testid={`model-try-${m.model_name}`}
                    onClick={() =>
                      navigate(
                        `/console/v2/playground?prefill_model=${encodeURIComponent(m.model_name)}`,
                      )
                    }
                  >
                    {tr('console.models.try_btn', 'try')} ↗
                  </button>
                  {/* single-model enable/disable deferred to v3 */}
                  <WIPBanner
                    reason={tr(
                      'console.models.wip_single_toggle',
                      'per-model enable/disable deferred to v3',
                    )}
                    todo='v3 story: per-model enable/disable'
                  />
                </div>
              </div>
            ))}
            {models.length === 0 && !loading && (
              <div
                data-testid='models-empty'
                style={{
                  gridColumn: '1/-1',
                  padding: 48,
                  textAlign: 'center',
                  color: 'var(--hf-ink-2)',
                }}
              >
                {tr('console.models.empty', 'no models yet')}
              </div>
            )}
          </div>
        )}
      </HFShell>

      {/* ── Add Model dialog ─────────────────────────────────────────────── */}
      {/* Native <dialog> — progressive enhancement; no external Modal dep needed. */}
      <dialog
        ref={dialogRef}
        data-testid='models-add-dialog'
        style={{
          padding: 32,
          borderRadius: 6,
          border: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
          color: 'var(--hf-ink)',
          minWidth: 400,
          maxWidth: 520,
        }}
        onClose={() => setAddOpen(false)}
      >
        <h2 style={{ margin: '0 0 20px', fontSize: 18 }}>
          {tr('console.models.add_title', 'Add model')}
        </h2>
        <form method='dialog' onSubmit={handleAddSubmit}>
          <div style={{ display: 'grid', gap: 14 }}>
            <label>
              <span
                className='lbl'
                style={{ display: 'block', marginBottom: 4 }}
              >
                {tr('console.models.field_name', 'model name')} *
              </span>
              <input
                data-testid='add-model-name'
                type='text'
                required
                value={form.model_name}
                onChange={(e) =>
                  setForm({ ...form, model_name: e.target.value })
                }
                placeholder={tr('console.models.ph_name', 'e.g. gpt-4o')}
                style={{
                  width: '100%',
                  padding: '6px 10px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                  fontFamily: 'var(--hf-mono)',
                  boxSizing: 'border-box',
                }}
              />
            </label>

            <label>
              <span
                className='lbl'
                style={{ display: 'block', marginBottom: 4 }}
              >
                {tr('console.models.vendor', 'vendor')}
              </span>
              <select
                data-testid='add-model-vendor'
                value={form.vendor}
                onChange={(e) => setForm({ ...form, vendor: e.target.value })}
                style={{
                  width: '100%',
                  padding: '6px 10px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                }}
              >
                <option value=''>
                  {tr('console.models.ph_vendor', '— select vendor —')}
                </option>
                {KNOWN_VENDORS.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>

            <label>
              <span
                className='lbl'
                style={{ display: 'block', marginBottom: 4 }}
              >
                {tr('console.models.field_quota_type', 'billing type')}
              </span>
              <select
                data-testid='add-model-quota-type'
                value={form.quota_type}
                onChange={(e) =>
                  setForm({ ...form, quota_type: Number(e.target.value) })
                }
                style={{
                  width: '100%',
                  padding: '6px 10px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                }}
              >
                {QUOTA_TYPE_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {tr(`console.models.${o.key}`, o.fallback)}
                  </option>
                ))}
              </select>
            </label>

            <label>
              <span
                className='lbl'
                style={{ display: 'block', marginBottom: 4 }}
              >
                {tr('console.models.field_ratio', 'model ratio')}
              </span>
              <input
                data-testid='add-model-ratio'
                type='number'
                min='0'
                step='0.001'
                value={form.model_ratio}
                onChange={(e) =>
                  setForm({ ...form, model_ratio: e.target.value })
                }
                style={{
                  width: '100%',
                  padding: '6px 10px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                  fontFamily: 'var(--hf-mono)',
                  boxSizing: 'border-box',
                }}
              />
            </label>

            {Number(form.quota_type) === 1 && (
              <label>
                <span
                  className='lbl'
                  style={{ display: 'block', marginBottom: 4 }}
                >
                  {tr('console.models.field_price', 'price per call')}
                </span>
                <input
                  data-testid='add-model-price'
                  type='number'
                  min='0'
                  step='0.001'
                  value={form.model_price}
                  onChange={(e) =>
                    setForm({ ...form, model_price: e.target.value })
                  }
                  style={{
                    width: '100%',
                    padding: '6px 10px',
                    border: '1px solid var(--hf-rule)',
                    background: 'var(--hf-sunken)',
                    color: 'var(--hf-ink)',
                    borderRadius: 2,
                    fontFamily: 'var(--hf-mono)',
                    boxSizing: 'border-box',
                  }}
                />
              </label>
            )}
          </div>

          <div
            style={{
              display: 'flex',
              justifyContent: 'flex-end',
              gap: 10,
              marginTop: 24,
            }}
          >
            <button
              type='button'
              className='btn'
              data-testid='add-model-cancel'
              onClick={() => {
                setAddOpen(false);
                clearDraft();
              }}
            >
              {tr('console.common.cancel', 'cancel')}
            </button>
            <button
              type='submit'
              className='btn primary'
              data-testid='add-model-submit'
              disabled={adding}
              onClick={handleAddSubmit}
            >
              {adding
                ? tr('console.models.adding', 'adding…')
                : tr('console.models.add', 'add')}
            </button>
          </div>
        </form>
      </dialog>
    </>
  );
};

export default HFModels;
