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
import { API, showError, showSuccess } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

// Reads tenant slug from localStorage — same pattern as Token/Channel/Models pages.

const fmtTime = (ts) => {
  if (!ts || ts === 0) return '—';
  return new Date(ts * 1000).toLocaleString();
};

// Labels resolved at render via tr() — module scope has no i18n context.
const statusLabel = (status, tr) => {
  if (status === 1)
    return tr('console.redemption.status_available', 'available');
  if (status === 3) return tr('console.redemption.status_used', 'used');
  return tr('console.redemption.status_disabled', 'disabled');
};

const statusClass = (status) => {
  if (status === 1) return 'tag ok';
  if (status === 3) return 'tag';
  return 'tag';
};

// ─── Create modal ─────────────────────────────────────────────────────────────

const CreateModal = ({ tenantSlug, onCreated, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({ name: '', count: 1, quota: 500000 });
  const [saving, setSaving] = useState(false);
  const nameRef = useRef(null);

  useEffect(() => {
    nameRef.current?.focus();
  }, []);

  const submit = async (e) => {
    e.preventDefault();
    if (!form.name.trim()) return;
    setSaving(true);
    try {
      const res = await API.post(`/api/v2/${tenantSlug}/redemptions`, {
        name: form.name.trim(),
        count: Number(form.count),
        quota: Number(form.quota),
      });
      if (res?.data?.success) {
        const codes = res.data.data.codes ?? [];
        showSuccess(
          tr('console.redemption.toast_created', 'Redemption codes generated'),
        );
        onCreated(codes);
      }
    } catch (_) {
      // error toast handled by API interceptor
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
          width: 400,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div className='strong' style={{ fontSize: 15 }}>
          {tr('console.redemption.modal_title', 'New redemption code')}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.redemption.field_name', 'name *')}
          </span>
          <input
            ref={nameRef}
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
            placeholder={tr('console.redemption.ph_name', 'e.g. promo-2025')}
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
            data-testid='redemption-name-input'
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.redemption.field_count', 'count (max 100)')}
          </span>
          <input
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
            type='number'
            min='1'
            max='100'
            value={form.count}
            onChange={(e) => setForm((f) => ({ ...f, count: e.target.value }))}
            data-testid='redemption-count-input'
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.redemption.field_quota', 'quota (quota units)')}
          </span>
          <input
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
            type='number'
            min='1'
            value={form.quota}
            onChange={(e) => setForm((f) => ({ ...f, quota: e.target.value }))}
            data-testid='redemption-quota-input'
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
          <button
            type='submit'
            className='btn primary'
            disabled={saving}
            data-testid='redemption-create-submit'
          >
            {saving
              ? tr('console.redemption.creating', 'generating…')
              : tr('console.redemption.create_submit', 'generate codes')}
          </button>
        </div>
      </form>
    </div>
  );
};

// ─── Newly created keys banner ─────────────────────────────────────────────────

const CreatedKeysBanner = ({ codes, onClose }) => {
  // Hook must run unconditionally (rules-of-hooks) — before the early return.
  const { t: tr } = useTranslation();
  if (!codes || codes.length === 0) return null;
  return (
    <div
      style={{
        marginBottom: 20,
        padding: '14px 16px',
        border: '1px solid var(--hf-ok)',
        background: 'rgba(31,122,79,0.06)',
        borderRadius: 4,
      }}
    >
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 10,
        }}
      >
        <span
          className='strong'
          style={{ fontSize: 12, color: 'var(--hf-ok)' }}
        >
          {tr(
            'console.redemption.created_banner',
            'Generated — copy now; original keys cannot be viewed again after closing',
          )}
        </span>
        <button
          type='button'
          className='btn ghost sm'
          onClick={onClose}
          data-testid='redemption-keys-close'
        >
          ✕
        </button>
      </div>
      <div
        style={{ display: 'flex', flexDirection: 'column', gap: 6 }}
        data-testid='redemption-keys-list'
      >
        {codes.map((c) => (
          <div
            key={c.id ?? c.key}
            style={{ display: 'flex', alignItems: 'center', gap: 10 }}
          >
            <span className='lbl' style={{ minWidth: 80 }}>
              {c.name}
            </span>
            <code
              className='mono'
              style={{ fontSize: 11, flex: 1, wordBreak: 'break-all' }}
            >
              {c.key}
            </code>
          </div>
        ))}
      </div>
    </div>
  );
};

// ─── Main page ────────────────────────────────────────────────────────────────

const HFRedemption = () => {
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` per the v2 console convention (avoids shadowing).
  const { t: tr } = useTranslation();

  const [redemptions, setRedemptions] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [newCodes, setNewCodes] = useState(null); // codes shown in banner after create
  const [deleteTarget, setDeleteTarget] = useState(null); // { id, name }

  const fetchRedemptions = useCallback(async () => {
    if (!tenantSlug || tenantSlug === 'default') return;
    setLoading(true);
    try {
      const res = await API.get(
        `/api/v2/${tenantSlug}/redemptions?page=1&page_size=50`,
      );
      if (res?.data?.success) {
        const d = res.data.data;
        setRedemptions(d.redemptions ?? []);
        setTotal(d.total ?? 0);
      }
    } catch (_) {
      // error toast handled by API interceptor
    } finally {
      setLoading(false);
    }
  }, [tenantSlug]);

  useEffect(() => {
    fetchRedemptions();
  }, [fetchRedemptions]);

  const handleCreated = async (codes) => {
    setCreating(false);
    setNewCodes(codes);
    await fetchRedemptions();
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      const res = await API.delete(
        `/api/v2/${tenantSlug}/redemptions/${deleteTarget.id}`,
      );
      if (res?.data?.success) {
        showSuccess(
          tr('console.redemption.toast_deleted', 'Redemption code deleted'),
        );
        setDeleteTarget(null);
        await fetchRedemptions();
      }
    } catch (_) {
      // error toast handled by API interceptor
    }
  };

  return (
    <HFShell
      active='redemption'
      crumbs={[
        tr('console.redemption.crumb_section', 'admin'),
        tr('console.redemption.crumb', 'redemption codes'),
      ]}
      actions={
        <>
          {loading ? (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.common.loading', 'loading…')}
            </span>
          ) : (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.redemption.total_count', { count: total })}
            </span>
          )}
          <button
            type='button'
            className='btn primary'
            onClick={() => setCreating(true)}
            data-testid='redemption-create-btn'
          >
            {tr('console.redemption.new_btn', '+ new code')}
          </button>
        </>
      }
    >
      <div style={{ padding: 24 }}>
        <CreatedKeysBanner codes={newCodes} onClose={() => setNewCodes(null)} />

        {!loading && redemptions.length === 0 && (
          <div
            className='muted'
            style={{ fontSize: 13, padding: '40px 0' }}
            data-testid='redemption-empty'
          >
            {tr(
              'console.redemption.empty',
              'No redemption codes yet. Click "+ new code" to generate.',
            )}
          </div>
        )}

        {redemptions.length > 0 && (
          <table
            style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}
          >
            <thead>
              <tr style={{ borderBottom: '1px solid var(--hf-rule)' }}>
                {[
                  tr('console.redemption.th_key', 'key'),
                  tr('console.redemption.th_name', 'name'),
                  tr('console.redemption.th_quota', 'quota'),
                  tr('console.redemption.th_status', 'status'),
                  tr('console.redemption.th_created', 'created'),
                  tr('console.redemption.th_expires', 'expires'),
                  tr('console.redemption.th_used_by', 'used by'),
                  tr('console.redemption.th_actions', 'actions'),
                ].map((h) => (
                  <th
                    key={h}
                    className='lbl'
                    style={{ padding: '8px 10px', textAlign: 'left' }}
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {redemptions.map((r) => (
                <tr
                  key={r.id}
                  style={{ borderBottom: '1px solid var(--hf-rule)' }}
                  data-testid={`redemption-row-${r.id}`}
                >
                  <td
                    className='mono'
                    style={{ padding: '8px 10px', fontSize: 11 }}
                  >
                    {r.key}
                  </td>
                  <td style={{ padding: '8px 10px' }}>{r.name}</td>
                  <td className='mono' style={{ padding: '8px 10px' }}>
                    {r.quota}
                  </td>
                  <td style={{ padding: '8px 10px' }}>
                    <span className={statusClass(r.status)}>
                      {statusLabel(r.status, tr)}
                    </span>
                  </td>
                  <td className='mono' style={{ padding: '8px 10px' }}>
                    {fmtTime(r.created_time)}
                  </td>
                  <td className='mono' style={{ padding: '8px 10px' }}>
                    {r.expired_time
                      ? fmtTime(r.expired_time)
                      : tr('console.redemption.never', 'never')}
                  </td>
                  <td className='mono' style={{ padding: '8px 10px' }}>
                    {r.used_user_id ? `#${r.used_user_id}` : '—'}
                  </td>
                  <td style={{ padding: '8px 10px' }}>
                    <button
                      type='button'
                      className='btn ghost sm'
                      style={{
                        color: 'var(--hf-err)',
                        borderColor: 'var(--hf-err)',
                      }}
                      onClick={() =>
                        setDeleteTarget({ id: r.id, name: r.name })
                      }
                      data-testid={`redemption-delete-btn-${r.id}`}
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

      {creating && (
        <CreateModal
          tenantSlug={tenantSlug}
          onCreated={handleCreated}
          onClose={() => setCreating(false)}
        />
      )}

      <ConfirmDialog
        visible={!!deleteTarget}
        title={tr(
          'console.redemption.confirm_delete_title',
          'Delete redemption code "{{name}}"?',
          { name: deleteTarget?.name ?? '' },
        )}
        consequenceList={[
          tr(
            'console.redemption.confirm_delete_consequence',
            'The code will be permanently deleted — this cannot be undone',
          ),
        ]}
        confirmText={deleteTarget?.name ?? ''}
        confirmButtonText={tr('console.common.delete', 'delete')}
        onConfirm={handleDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </HFShell>
  );
};

export default HFRedemption;
