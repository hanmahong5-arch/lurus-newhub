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
import ConfirmDialog from '../../../../components/common/ConfirmDialog';
import { API, showSuccess } from '../../../../helpers';

/*
 * Per-model rate limits admin (migration 026 / middleware.BusinessModelRateLimit).
 * CRUD over the (tenant, model) → rpm/tpm rows behind
 *   GET/PUT  /api/v2/admin/tenants/:id/model-limits
 *   DELETE   /api/v2/admin/tenants/:id/model-limits?model=<name>
 * The token and tenant dimensions get their config surfaces on the Token and
 * Tenants pages; this is the third (model) dimension of the same limiter.
 * JSON keys mirror entity.ModelRateLimit tags (rate_limit_rpm/rate_limit_tpm);
 * 0 = unlimited, exactly like the tenant-level caps.
 */

// The bootstrap tenant is always addressable even without a tenants row (the
// backend carves it out); single-tenant installs set per-model caps here first.
const DEFAULT_TENANT = 'default';

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

// ─── Add / edit modal ─────────────────────────────────────────────────────────
// `target.isNew` distinguishes create (model editable) from edit (model is the
// row key — renaming would orphan the old row, so it is delete-then-create).

const LimitModal = ({ tenantId, target, onSaved, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({
    model: target.model || '',
    rpm: target.rate_limit_rpm > 0 ? String(target.rate_limit_rpm) : '',
    tpm: target.rate_limit_tpm > 0 ? String(target.rate_limit_tpm) : '',
  });
  const [saving, setSaving] = useState(false);

  const submit = async (e) => {
    e.preventDefault();
    const model = form.model.trim();
    if (!model) return;
    setSaving(true);
    try {
      const res = await API.put(
        `/api/v2/admin/tenants/${tenantId}/model-limits`,
        {
          model,
          rate_limit_rpm: Math.max(0, parseInt(form.rpm, 10) || 0),
          rate_limit_tpm: Math.max(0, parseInt(form.tpm, 10) || 0),
        },
      );
      if (res?.data?.success) {
        showSuccess(
          tr('console.model_limits.toast_saved', 'Model limit saved'),
        );
        onSaved();
      }
    } catch (_) {
      // error toast shown by the API interceptor
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
          {target.isNew
            ? tr('console.model_limits.modal_new', 'New model limit')
            : tr('console.model_limits.modal_edit', 'Edit model limit')}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.model_limits.field_model', 'model name')}
          </span>
          <input
            data-testid='mrl-model'
            style={{
              ...inputStyle,
              opacity: target.isNew ? 1 : 0.6,
              cursor: target.isNew ? 'text' : 'not-allowed',
            }}
            value={form.model}
            disabled={!target.isNew}
            autoFocus={target.isNew}
            placeholder='gpt-4o'
            onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
          />
          <span className='muted' style={{ fontSize: 11 }}>
            {tr(
              'console.model_limits.field_model_hint',
              'Exact model name as requested (e.g. gpt-4o). No wildcards.',
            )}
          </span>
        </label>

        <div
          style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}
        >
          {[
            ['rpm', tr('console.model_limits.field_rpm', 'rpm limit')],
            ['tpm', tr('console.model_limits.field_tpm', 'tpm limit')],
          ].map(([k, label]) => (
            <label
              key={k}
              style={{ display: 'flex', flexDirection: 'column', gap: 5 }}
            >
              <span className='lbl'>{label}</span>
              <input
                data-testid={`mrl-${k}`}
                style={inputStyle}
                type='number'
                min='0'
                placeholder='0'
                value={form[k]}
                onChange={(e) =>
                  setForm((f) => ({ ...f, [k]: e.target.value }))
                }
              />
            </label>
          ))}
        </div>

        <div className='muted' style={{ fontSize: 11 }}>
          {tr(
            'console.model_limits.limits_hint',
            'Per-minute caps for this model under the tenant. 0 = unlimited.',
          )}
        </div>

        <div
          style={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 10,
            marginTop: 4,
          }}
        >
          <button type='button' className='btn ghost' onClick={onClose}>
            {tr('console.common.cancel', 'cancel')}
          </button>
          <button
            type='submit'
            className='btn primary'
            disabled={saving || !form.model.trim()}
            data-testid='mrl-save'
          >
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

const HFModelRateLimits = () => {
  const { t: tr } = useTranslation();
  const [tenants, setTenants] = useState([]);
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT);
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [editTarget, setEditTarget] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);

  // Tenant list drives the selector. `default` is always addressable (backend
  // carve-out) even when no tenants row exists, so keep it selectable.
  const fetchTenants = useCallback(async () => {
    try {
      const params = new URLSearchParams({ page: 1, page_size: 50 });
      const res = await API.get(`/api/v2/admin/tenants?${params}`);
      if (res?.data?.success) {
        const list = res.data.data.tenants ?? res.data.data.items ?? [];
        setTenants(list);
      }
    } catch (err) {
      if (err?.response?.status === 403) {
        setForbidden(true);
      }
      // A non-403 tenants failure still leaves the `default` tenant usable.
    }
  }, []);

  const fetchLimits = useCallback(async (tid) => {
    if (!tid) return;
    setLoading(true);
    setForbidden(false);
    try {
      const res = await API.get(`/api/v2/admin/tenants/${tid}/model-limits`);
      if (res?.data?.success) {
        setRows(res.data.data ?? []);
      }
    } catch (err) {
      if (err?.response?.status === 403) {
        setForbidden(true);
      }
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchTenants();
  }, [fetchTenants]);

  useEffect(() => {
    fetchLimits(tenantId);
  }, [tenantId, fetchLimits]);

  const doDelete = async () => {
    if (!deleteTarget) return;
    try {
      const res = await API.delete(
        `/api/v2/admin/tenants/${tenantId}/model-limits?model=${encodeURIComponent(
          deleteTarget.model,
        )}`,
      );
      if (res?.data?.success) {
        showSuccess(
          tr('console.model_limits.toast_deleted', 'Model limit deleted'),
        );
        setDeleteTarget(null);
        await fetchLimits(tenantId);
      }
    } catch (_) {
      // error toast from the interceptor
    }
  };

  const handleSaved = async () => {
    setEditTarget(null);
    await fetchLimits(tenantId);
  };

  // `default` must always be selectable even when absent from the tenants row set.
  const tenantOptions = [
    ...(tenants.some((t) => t.id === DEFAULT_TENANT)
      ? []
      : [{ id: DEFAULT_TENANT, name: DEFAULT_TENANT }]),
    ...tenants,
  ];

  const fmtLimit = (v) =>
    v > 0 ? String(v) : tr('console.model_limits.unlimited', 'unlimited');

  return (
    <HFShell
      active='admin-model-limits'
      crumbs={[
        tr('console.nav.section_platform_admin', 'platform · admin'),
        tr('console.model_limits.crumb', 'model limits'),
      ]}
      actions={
        !forbidden && (
          <>
            <select
              data-testid='mrl-tenant-select'
              value={tenantId}
              onChange={(e) => setTenantId(e.target.value)}
              style={{ ...inputStyle, width: 'auto' }}
            >
              {tenantOptions.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name || t.id}
                </option>
              ))}
            </select>
            <button
              type='button'
              className='btn primary'
              data-testid='mrl-new-btn'
              onClick={() => setEditTarget({ isNew: true })}
            >
              {tr('console.model_limits.new', '+ new model limit')}
            </button>
          </>
        )
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.model_limits.crumb', 'model limits')}
          </div>
          <h1>
            {loading
              ? '…'
              : forbidden
                ? tr(
                    'console.model_limits.admin_required',
                    'Admin access required',
                  )
                : tr('console.model_limits.count', '{{count}} model limits', {
                    count: rows.length,
                  })}
          </h1>
          <div className='sub'>
            {tr(
              'console.model_limits.sub',
              'per-tenant · per-model RPM / TPM caps · 0 = unlimited',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.model_limits.admin_required',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.model_limits.admin_required_body',
                'You do not have permission to manage model rate limits. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          <div className='panel'>
            {loading ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : rows.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
                data-testid='mrl-empty'
              >
                {tr(
                  'console.model_limits.empty',
                  'No per-model limits set for this tenant.',
                )}
              </div>
            ) : (
              <table
                className='hf-table'
                style={{ width: '100%', borderCollapse: 'collapse' }}
              >
                <thead>
                  <tr>
                    {[
                      tr('console.model_limits.col_model', 'model'),
                      tr('console.model_limits.col_rpm', 'rpm limit'),
                      tr('console.model_limits.col_tpm', 'tpm limit'),
                      '',
                    ].map((h, i) => (
                      <th
                        key={i}
                        className='lbl'
                        style={{
                          textAlign: i === 3 ? 'right' : 'left',
                          padding: '10px 16px',
                          borderBottom: '1px solid var(--hf-rule)',
                          fontSize: 11,
                        }}
                      >
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.id} data-testid={`mrl-row-${r.id}`}>
                      <td
                        className='mono strong'
                        style={{
                          padding: '10px 16px',
                          borderBottom: '1px dashed var(--hf-rule)',
                          fontSize: 12,
                        }}
                      >
                        {r.model}
                      </td>
                      <td
                        className='mono'
                        style={{
                          padding: '10px 16px',
                          borderBottom: '1px dashed var(--hf-rule)',
                          fontSize: 12,
                        }}
                      >
                        {fmtLimit(r.rate_limit_rpm)}
                      </td>
                      <td
                        className='mono'
                        style={{
                          padding: '10px 16px',
                          borderBottom: '1px dashed var(--hf-rule)',
                          fontSize: 12,
                        }}
                      >
                        {fmtLimit(r.rate_limit_tpm)}
                      </td>
                      <td
                        style={{
                          padding: '10px 16px',
                          borderBottom: '1px dashed var(--hf-rule)',
                          textAlign: 'right',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        <button
                          type='button'
                          className='btn ghost sm'
                          data-testid={`mrl-edit-btn-${r.id}`}
                          onClick={() => setEditTarget({ ...r, isNew: false })}
                        >
                          {tr('console.common.edit', 'edit')}
                        </button>{' '}
                        <button
                          type='button'
                          className='btn ghost sm'
                          data-testid={`mrl-delete-btn-${r.id}`}
                          onClick={() => setDeleteTarget(r)}
                        >
                          {tr('console.common.delete', 'delete')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}

      {editTarget && (
        <LimitModal
          tenantId={tenantId}
          target={editTarget}
          onSaved={handleSaved}
          onClose={() => setEditTarget(null)}
        />
      )}

      <ConfirmDialog
        visible={!!deleteTarget}
        title={tr(
          'console.model_limits.delete_confirm',
          'Delete the {{model}} limit for this tenant?',
          { model: deleteTarget?.model || '' },
        )}
        confirmText={deleteTarget?.model || ''}
        confirmButtonText={tr('console.common.delete', 'delete')}
        onConfirm={doDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </HFShell>
  );
};

export default HFModelRateLimits;
