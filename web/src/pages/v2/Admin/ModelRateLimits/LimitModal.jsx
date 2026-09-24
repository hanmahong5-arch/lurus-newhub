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
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API, showSuccess } from '../../../../helpers';

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

export default LimitModal;
export { inputStyle };
