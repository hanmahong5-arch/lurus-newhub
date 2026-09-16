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

  // ── Model availability (tenant model allow-list) ─────────────────────────
  // GET/PUT/DELETE /api/v2/admin/tenants/:id/model-allowlist
  // (internal/adapter/handler/tenant_model_allowlist.go), the neighbouring
  // capability to the rate limits above — same tenant selector, same
  // adminRoute (RootJWTAuth) gate, same `forbidden` fallback.
  // `allowlist` is the last-fetched server state (null while loading or on
  // a fetch that never came back); `draftList` is the locally-edited copy
  // the save button PUTs. `mode` (observe/enforce) is a platform-wide env
  // var (TENANT_MODEL_ALLOWLIST_MODE) the server reports — it is not
  // editable from here.
  const [allowlist, setAllowlist] = useState(null);
  const [allowlistLoading, setAllowlistLoading] = useState(true);
  // Set on any non-403 fetch failure (500, network, malformed 200). Unknown
  // is not "observe" and unknown is not "unconfigured" — the platform may
  // be in enforce mode with a restrictive row this page simply failed to
  // read, so a failure here must render as its own state, not fall back to
  // the same UI as "no allow-list configured" (which reads to an operator
  // as "every model reachable").
  const [allowlistError, setAllowlistError] = useState(false);
  const [draftList, setDraftList] = useState([]);
  const [newModelEntry, setNewModelEntry] = useState('');
  const [savingAllowlist, setSavingAllowlist] = useState(false);
  // Confirms the two allow-list actions that can lock every model out for a
  // tenant with one click: saving an explicitly empty list (deny-all, per
  // the PUT handler's own contract) and clearing the row back to
  // unrestricted. Kept separate from `deleteTarget` (a different resource,
  // the per-model rate-limit row) so the two dialogs never fight over state.
  const [allowlistAction, setAllowlistAction] = useState(null);

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

  // Always holds the currently-selected tenant, independent of render
  // timing — fetchAllowlist below compares its own `tid` against this after
  // every await so a response for a tenant the operator has since switched
  // away from cannot overwrite the newer selection's state (and cannot PUT
  // the stale tenant's models over the new one on save).
  const tenantIdRef = useRef(tenantId);
  useEffect(() => {
    tenantIdRef.current = tenantId;
  }, [tenantId]);

  const fetchAllowlist = useCallback(async (tid) => {
    if (!tid) return;
    setAllowlistLoading(true);
    setAllowlistError(false);
    try {
      const res = await API.get(`/api/v2/admin/tenants/${tid}/model-allowlist`);
      if (tid !== tenantIdRef.current) return; // superseded by a later selection
      if (res?.data?.success) {
        const data = res.data.data ?? {};
        setAllowlist(data);
        setDraftList(
          Array.isArray(data.allowed_models) ? data.allowed_models : [],
        );
      } else {
        // 200 but success:false — the read did not actually work; treat it
        // the same as a thrown error rather than as "no allow-list".
        setAllowlist(null);
        setDraftList([]);
        setAllowlistError(true);
      }
      setAllowlistLoading(false);
    } catch (err) {
      if (tid !== tenantIdRef.current) return; // superseded by a later selection
      if (err?.response?.status === 403) {
        setForbidden(true);
      } else {
        // Any non-403 failure (500, network, timeout): unknown state, not
        // "observe" and not "unconfigured". Leaves allowlist=null and
        // draftList=[] but allowlistError=true means the render below shows
        // an explicit error, not the unrestricted-tenant defaults, and the
        // save/clear controls stay disabled — an empty draftList must never
        // reach a PUT here, or a read failure becomes a silent deny-all.
        setAllowlist(null);
        setDraftList([]);
        setAllowlistError(true);
      }
      setAllowlistLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchTenants();
  }, [fetchTenants]);

  useEffect(() => {
    fetchLimits(tenantId);
    fetchAllowlist(tenantId);
  }, [tenantId, fetchLimits, fetchAllowlist]);

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

  // ── Allow-list draft editing ──────────────────────────────────────────────
  const addDraftEntry = () => {
    const v = newModelEntry.trim();
    if (!v || draftList.includes(v)) return;
    setDraftList((l) => [...l, v]);
    setNewModelEntry('');
  };

  const removeDraftEntry = (model) => {
    setDraftList((l) => l.filter((m) => m !== model));
  };

  const putAllowlist = async () => {
    setSavingAllowlist(true);
    try {
      const res = await API.put(
        `/api/v2/admin/tenants/${tenantId}/model-allowlist`,
        { allowed_models: draftList },
      );
      if (res?.data?.success) {
        showSuccess(
          tr(
            'console.model_limits.availability_toast_saved',
            'Model allow-list saved',
          ),
        );
        await fetchAllowlist(tenantId);
      }
    } catch (_) {
      // error toast from the interceptor
    } finally {
      setSavingAllowlist(false);
    }
  };

  const deleteAllowlist = async () => {
    try {
      const res = await API.delete(
        `/api/v2/admin/tenants/${tenantId}/model-allowlist`,
      );
      if (res?.data?.success) {
        showSuccess(
          tr(
            'console.model_limits.availability_toast_cleared',
            'Model allow-list cleared — tenant is unrestricted',
          ),
        );
        await fetchAllowlist(tenantId);
      }
    } catch (_) {
      // error toast from the interceptor
    }
  };

  // Saving an explicitly empty list is a valid, deliberate deny-all (the PUT
  // handler's own contract — tenant_model_allowlist.go:66-68) but is also the
  // one-click way to lock every model out for this tenant, so it goes through
  // the same typed-confirm dialog as clearing the row.
  const requestSaveAllowlist = () => {
    if (draftList.length === 0) {
      setAllowlistAction({ kind: 'save-empty' });
      return;
    }
    putAllowlist();
  };

  const requestClearAllowlist = () => setAllowlistAction({ kind: 'clear' });

  const confirmAllowlistAction = async () => {
    if (allowlistAction?.kind === 'save-empty') await putAllowlist();
    else if (allowlistAction?.kind === 'clear') await deleteAllowlist();
    setAllowlistAction(null);
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
        tr('console.nav.section_governance', 'governance'),
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

      {!forbidden && (
        <div style={{ padding: '0 24px 24px' }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 4 }}>
              {tr(
                'console.model_limits.availability_title',
                'model availability',
              )}
            </div>
            <div className='muted' style={{ fontSize: 11, marginBottom: 14 }}>
              {tr(
                'console.model_limits.availability_sub',
                'per-tenant model allow-list · mode is set platform-wide by the operator, not per tenant',
              )}
            </div>

            {allowlistLoading ? (
              <div className='muted' style={{ fontSize: 12 }}>
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : (
              <>
                {allowlistError ? (
                  // Unknown is not "observe" and unknown is not
                  // "unconfigured" — a non-403 GET failure (500, network,
                  // malformed 200) must not fall through to the defaults
                  // below, or an enforce-mode tenant with a real
                  // restrictive row reads as "every model reachable".
                  <div
                    data-testid='mrl-availability-error'
                    className='muted'
                    style={{
                      fontSize: 12,
                      marginBottom: 12,
                      color: 'var(--hf-warn)',
                      border: '1px solid var(--hf-warn)',
                      borderRadius: 2,
                      padding: '8px 10px',
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                    }}
                  >
                    <span>
                      {tr(
                        'console.model_limits.availability_error',
                        'Could not read the allow-list for this tenant — state unknown, not editable.',
                      )}
                    </span>
                    <button
                      type='button'
                      className='btn ghost sm'
                      data-testid='mrl-availability-retry'
                      onClick={() => fetchAllowlist(tenantId)}
                    >
                      {tr('console.model_limits.availability_retry', 'retry')}
                    </button>
                  </div>
                ) : (
                  <>
                    <div
                      data-testid='mrl-availability-mode'
                      data-mode={allowlist?.mode || 'observe'}
                      style={{
                        fontSize: 12,
                        fontFamily: 'var(--hf-mono)',
                        padding: '6px 10px',
                        marginBottom: 12,
                        border: `1px solid ${
                          allowlist?.mode === 'enforce'
                            ? 'var(--hf-warn)'
                            : 'var(--hf-rule)'
                        }`,
                        color:
                          allowlist?.mode === 'enforce'
                            ? 'var(--hf-warn)'
                            : 'var(--hf-ink-2)',
                        borderRadius: 2,
                        display: 'inline-block',
                      }}
                    >
                      {allowlist?.mode === 'enforce'
                        ? tr(
                            'console.model_limits.mode_enforce',
                            'enforce — a model off the list is refused (HTTP 403)',
                          )
                        : tr(
                            'console.model_limits.mode_observe',
                            'observe — a model off the list still answers; the miss is only counted',
                          )}
                    </div>

                    {!allowlist?.configured && (
                      <div
                        data-testid='mrl-availability-unrestricted'
                        className='muted'
                        style={{ fontSize: 12, marginBottom: 12 }}
                      >
                        {tr(
                          'console.model_limits.availability_unrestricted',
                          'No allow-list configured — every catalog model is reachable for this tenant.',
                        )}
                      </div>
                    )}
                  </>
                )}

                <div
                  style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    gap: 6,
                    marginBottom: 12,
                  }}
                >
                  {draftList.map((m) => (
                    <span
                      key={m}
                      data-testid={`mrl-availability-entry-${m}`}
                      className='mono'
                      style={{
                        fontSize: 11,
                        border: '1px solid var(--hf-rule)',
                        borderRadius: 2,
                        padding: '3px 8px',
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: 6,
                      }}
                    >
                      {m}
                      <button
                        type='button'
                        className='btn ghost sm'
                        data-testid={`mrl-availability-remove-${m}`}
                        onClick={() => removeDraftEntry(m)}
                        style={{ padding: '0 4px', fontSize: 11 }}
                      >
                        ×
                      </button>
                    </span>
                  ))}
                </div>

                <div style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
                  <input
                    data-testid='mrl-availability-add-input'
                    style={{ ...inputStyle, width: 260 }}
                    placeholder={tr(
                      'console.model_limits.availability_placeholder',
                      'gpt-4o or gpt-4o-*',
                    )}
                    value={newModelEntry}
                    disabled={allowlistError}
                    onChange={(e) => setNewModelEntry(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault();
                        addDraftEntry();
                      }
                    }}
                  />
                  <button
                    type='button'
                    className='btn ghost sm'
                    data-testid='mrl-availability-add-btn'
                    disabled={allowlistError}
                    onClick={addDraftEntry}
                  >
                    {tr('console.model_limits.availability_add', '+ add')}
                  </button>
                </div>

                {draftList.length === 0 && !allowlistError && (
                  <div
                    className='muted'
                    style={{ fontSize: 11, marginBottom: 10 }}
                  >
                    {tr(
                      'console.model_limits.availability_empty_warning',
                      'Saving an empty list denies every model for this tenant — it is not the same as leaving the allow-list unconfigured.',
                    )}
                  </div>
                )}

                <div style={{ display: 'flex', gap: 10 }}>
                  <button
                    type='button'
                    className='btn primary'
                    data-testid='mrl-availability-save'
                    disabled={savingAllowlist || allowlistError}
                    onClick={requestSaveAllowlist}
                  >
                    {savingAllowlist
                      ? tr('console.common.loading', 'Loading…')
                      : tr(
                          'console.model_limits.availability_save',
                          'save allow-list',
                        )}
                  </button>
                  <button
                    type='button'
                    className='btn ghost'
                    data-testid='mrl-availability-clear'
                    disabled={!allowlist?.configured || allowlistError}
                    onClick={requestClearAllowlist}
                  >
                    {tr(
                      'console.model_limits.availability_clear',
                      'clear (make unrestricted)',
                    )}
                  </button>
                </div>
              </>
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

      <ConfirmDialog
        visible={!!allowlistAction}
        title={
          allowlistAction?.kind === 'clear'
            ? tr(
                'console.model_limits.clear_confirm_title',
                'Remove the allow-list for {{tenant}}? The tenant becomes unrestricted.',
                { tenant: tenantId },
              )
            : tr(
                'console.model_limits.save_empty_confirm_title',
                'Save an EMPTY allow-list for {{tenant}}? This denies every model.',
                { tenant: tenantId },
              )
        }
        confirmText={tenantId}
        confirmButtonText={tr('console.common.confirm', 'confirm')}
        onConfirm={confirmAllowlistAction}
        onCancel={() => setAllowlistAction(null)}
      />
    </HFShell>
  );
};

export default HFModelRateLimits;
