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
import { API, showSuccess } from '../../../helpers';

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

export default LimitsModal;
