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
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import {
  API,
  getServerAddress,
  isRoot,
  showError,
  showSuccess,
} from '../../../helpers';
import { useFormDraft } from '../../../hooks/common/useFormDraft';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import { useTenantModels } from '../../../hooks/models/useTenantModels';
import { useRoutableModels } from '../../../hooks/models/useRoutableModels';
import Marketplace from './Marketplace';
import { buildCatalog } from './catalog';

/* HiFi 7 — Models catalog. Wired to GET /api/v2/:tenant_slug/models (2026-05-19).
   Wave 3 Phase 1 (2026-05-20): add-model modal + try ↗ navigate wired.
   Per-model enable/disable (2026-09-16): the write side of the tenant model
   allow-list (internal/app/tenantpolicy, GET/PUT/DELETE
   /api/v2/admin/tenants/:id/model-allowlist) is root-gated (RootJWTAuth),
   while this page sits behind UserAuth + TenantSlugGuard — a non-root caller
   cannot reach that endpoint and this page must not pretend otherwise. The
   control itself lives on Admin/ModelRateLimits (the neighbouring
   per-tenant/per-model admin surface); this page only reads the allow-list
   (root only — GET /:tenant_slug/user/me for tenant_id, then the admin GET)
   to render each model's real allow-list state, plus a link to the admin
   page for role >= 100. A non-root viewer keeps seeing the model's existing
   catalog status (m.status, below) — real data this page already had — with
   no banner claiming the capability doesn't exist. */

// Mirrors the match rule in internal/app/tenantpolicy.ModelAllowed (exact
// name, or a trailing "*" prefix) against the tenant's allow-list. It does
// NOT reproduce that function's ratio_setting.FormatMatchingModelName
// normalization step (request-time model-name canonicalisation), so a
// wildcard entry that depends on normalization to match may render
// differently here than it resolves on the relay path.
export function modelMatchesAllowlist(list, modelName) {
  for (const raw of list || []) {
    const entry = (raw || '').trim();
    if (!entry) continue;
    if (entry.endsWith('*')) {
      if (modelName.startsWith(entry.slice(0, -1))) return true;
    } else if (entry === modelName) {
      return true;
    }
  }
  return false;
}

// Reduces a fetched allow-list ({configured, allowed_models, mode}) plus a
// model name to one of four states. 'blocked' and 'observed' are the pair
// the observe/enforce oracle in this file's tests exists to keep visibly
// distinct — an 'observed' model still answers relay requests (only
// counted, per internal/adapter/middleware/distributor.go), while a
// 'blocked' one is refused (403); labelling both the same way would be
// exactly the kind of lie this cycle removes.
export function describeModelAvailability(allowlist, modelName) {
  if (!allowlist?.configured) return 'unrestricted';
  if (modelMatchesAllowlist(allowlist.allowed_models, modelName))
    return 'allowed';
  return allowlist.mode === 'enforce' ? 'blocked' : 'observed';
}

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

// Root-only: the tenant allow-list state of one model (see file header).
const Availability = ({ allowlist, id, tr }) => {
  const state = describeModelAvailability(allowlist, id);
  return (
    <span
      className={'tag' + (state === 'blocked' ? ' warn' : '')}
      data-testid={`model-availability-${id}`}
      data-availability={state}
    >
      {
        {
          unrestricted: tr(
            'console.models.availability_unrestricted',
            'tenant allow-list: unrestricted',
          ),
          allowed: tr(
            'console.models.availability_allowed',
            'on tenant allow-list',
          ),
          observed: tr(
            'console.models.availability_observed',
            'off tenant allow-list — still answers (observe mode)',
          ),
          blocked: tr(
            'console.models.availability_blocked',
            'blocked by tenant allow-list (enforce mode)',
          ),
        }[state]
      }
    </span>
  );
};

const HFModels = () => {
  const tenantSlug = useTenantSlug();
  const navigate = useNavigate();
  // Aliased to `tr` per the v2 console convention.
  const { t: tr } = useTranslation();
  // role >= 100 per web/src/helpers/utils.jsx:isRoot — the same client-side
  // gate SiderBar.jsx and UserInfoHeader.jsx use to decide what to show a
  // root viewer (NOT CommandPalette: that reads role through
  // useBridgedUser()/visibleNavItems — components/hifi/HFShell.jsx — a
  // separate, try/catch-guarded read of the same localStorage key). It is a
  // UX pre-check only: the actual enforcement is the admin endpoint's
  // RootJWTAuth, which the fetch below still has to clear.
  // isRoot() itself does an unguarded JSON.parse(localStorage.user); this
  // page can't change that helper, so the call is wrapped here — a
  // malformed payload degrades to "not root" instead of white-screening
  // this page's render.
  let rootUser = false;
  try {
    rootUser = isRoot();
  } catch (_) {
    rootUser = false;
  }

  const {
    items: models,
    loading,
    error: modelsError,
    refetch: refetchModels,
  } = useTenantModels(tenantSlug, { limit: 100, offset: 0 });
  const { items: routable, loading: routableLoading } = useRoutableModels(
    tenantSlug,
    { skipErrorHandler: true },
  );

  // Prices (ratios → $/1M in catalog.js) and the caller's group multiplier.
  const [pricing, setPricing] = useState({ rows: [], groupRatio: {} });
  // Tokens/requests per model over 7 days. The rankings endpoint is
  // tenant-admin gated server-side; for anyone else this stays empty and the
  // cards read 0 rather than inventing a number.
  const [usage, setUsage] = useState([]);
  useEffect(() => {
    if (!tenantSlug) return undefined;
    let cancelled = false;
    API.get(`/api/v2/${tenantSlug}/pricing`, { skipErrorHandler: true })
      .then((res) => {
        if (cancelled || !res?.data?.success) return;
        setPricing({
          rows: res.data.data?.pricing || [],
          groupRatio: res.data.data?.group_ratio || {},
        });
      })
      .catch(() => {});
    API.get(`/api/v2/${tenantSlug}/analytics/rankings?by=model&hours=168`, {
      skipErrorHandler: true,
    })
      .then((res) => {
        if (!cancelled && res?.data?.success)
          setUsage(res.data.data?.rows || []);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [tenantSlug]);

  const userGroup = useMemo(() => {
    try {
      return (
        JSON.parse(localStorage.getItem('user') || '{}').group || 'default'
      );
    } catch (_) {
      return 'default';
    }
  }, []);
  const entries = useMemo(
    () =>
      buildCatalog({
        routable,
        pricing: pricing.rows,
        catalogue: models,
        usage,
        groupRatio: pricing.groupRatio[userGroup] ?? 1,
      }),
    [routable, pricing, models, usage, userGroup],
  );
  const callable = entries.filter((e) => e.routable).length;

  // Surface load failures as one toast per failed fetch, message from the
  // response body when the backend sent one. Before the shared hook the page
  // toasted only on a rejected request; a 200 body with success:false left
  // the list empty and silent.
  useEffect(() => {
    if (!modelsError) return;
    const msg =
      modelsError?.response?.data?.message ??
      modelsError?.message ??
      (typeof modelsError === 'string' ? modelsError : null) ??
      tr('console.models.load_failed', 'Failed to load models');
    showError(msg);
  }, [modelsError, tr]);

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

  // Tenant model allow-list (root only — see the file-header comment).
  // `allowlist` stays null for a non-root viewer (no fetch attempted) or on
  // any failure (403 from a stale client-side role, tenant lookup failure,
  // network error) — every render site below already treats null as "no
  // per-model allow-list state to show", not as "unrestricted".
  const [allowlist, setAllowlist] = useState(null);
  useEffect(() => {
    if (!rootUser || !tenantSlug) return undefined;
    let cancelled = false;
    (async () => {
      try {
        // skipErrorHandler: this is a decorative, root-only enrichment of a
        // page every user (root or not) can already load — a failure here
        // must not fire the global error toast/401-heal (helpers/api.js),
        // same reasoning as CommandPalette/index.jsx's identical skip on its
        // own best-effort model fetch.
        const selfRes = await API.get(`/api/v2/${tenantSlug}/user/me`, {
          skipErrorHandler: true,
        });
        const tid = selfRes?.data?.data?.tenant_id;
        if (cancelled || !selfRes?.data?.success || !tid) return;
        const alRes = await API.get(
          `/api/v2/admin/tenants/${tid}/model-allowlist`,
          { skipErrorHandler: true },
        );
        if (cancelled) return;
        if (alRes?.data?.success) setAllowlist(alRes.data.data);
      } catch (_) {
        // Root client-side but the server disagreed (403), or the tenant
        // lookup failed — either way, fall back to showing no allow-list
        // state rather than guessing at one.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [rootUser, tenantSlug]);

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
      refetchModels();
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
          tr('console.models.market.eyebrow', 'model marketplace'),
        ]}
        actions={
          <>
            {/* The write side (tenant model allow-list) is root-gated —
                only role >= 100 gets a control here; see the file-header
                comment for why this can't be widened to every user. */}
            {rootUser && (
              <button
                type='button'
                className='btn'
                data-testid='models-manage-availability-link'
                onClick={() => navigate('/console/v2/admin/model-limits')}
              >
                {tr(
                  'console.models.manage_availability',
                  'manage model availability →',
                )}
              </button>
            )}
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
              {tr('console.models.market.eyebrow', 'model marketplace')}
            </div>
            <h1>
              {loading && routableLoading && entries.length === 0
                ? '…'
                : entries.length}{' '}
              <span className='muted' style={{ fontWeight: 400 }}>
                {tr('console.models.unit_models', 'models')}
              </span>
            </h1>
            <div className='muted' style={{ fontSize: 13, marginTop: 4 }}>
              {tr(
                'console.models.market.subtitle',
                '{{n}} callable with your keys · prices per 1M tokens, after your group multiplier',
                { n: callable },
              )}
            </div>
          </div>
        </div>

        <Marketplace
          entries={entries}
          loading={loading || routableLoading}
          tr={tr}
          base={getServerAddress()}
          onTry={(id) =>
            navigate(
              `/console/v2/playground?prefill_model=${encodeURIComponent(id)}`,
            )
          }
          onKeys={() => navigate('/console/v2/token')}
          onCopy={(text) => {
            navigator.clipboard?.writeText(text).then(
              () => showSuccess(tr('console.common.copied', 'copied')),
              () => {},
            );
          }}
          availabilityFor={
            rootUser && allowlist
              ? (id) => <Availability allowlist={allowlist} id={id} tr={tr} />
              : undefined
          }
        />
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
