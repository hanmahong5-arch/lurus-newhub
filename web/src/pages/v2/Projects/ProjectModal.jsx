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
import React, { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API, showSuccess } from '../../../helpers';

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

// ─── Create / edit modal ──────────────────────────────────────────────────────
// `target.isNew` distinguishes create from edit. Unlike the model-limits modal
// the key (name) IS editable on edit: historical log rows reference the numeric
// id, so a rename re-labels history rather than orphaning it.

const ProjectModal = ({ tenantSlug, target, onSaved, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({
    name: target.name || '',
    description: target.description || '',
  });
  const [saving, setSaving] = useState(false);
  // A ref, not the `saving` state: two submits fired in the same tick (Enter
  // plus a click) would both read the pre-render value of the state.
  const inFlight = useRef(false);

  const submit = async (e) => {
    e.preventDefault();
    const name = form.name.trim();
    if (!name || inFlight.current) return;
    inFlight.current = true;
    setSaving(true);
    try {
      const payload = { name, description: form.description.trim() };
      const res = target.isNew
        ? await API.post(`/api/v2/${tenantSlug}/projects`, payload)
        : await API.put(`/api/v2/${tenantSlug}/projects/${target.id}`, payload);
      if (res?.data?.success) {
        showSuccess(tr('console.projects.toast_saved', 'Project saved'));
        onSaved();
        return; // unmounted by the parent — leave inFlight latched
      }
    } catch (_) {
      // error toast shown by the API interceptor (409 duplicate name included)
    }
    inFlight.current = false;
    setSaving(false);
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
      onClick={(e) => e.target === e.currentTarget && !saving && onClose()}
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
            ? tr('console.projects.modal_new', 'New project')
            : tr('console.projects.modal_edit', 'Edit project')}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.projects.field_name', 'name *')}
          </span>
          <input
            data-testid='proj-name'
            style={inputStyle}
            value={form.name}
            autoFocus
            maxLength={128}
            disabled={saving}
            placeholder={tr('console.projects.ph_name', 'e.g. Marketing')}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.projects.field_description', 'description')}
          </span>
          <input
            data-testid='proj-description'
            style={inputStyle}
            value={form.description}
            maxLength={512}
            disabled={saving}
            placeholder={tr(
              'console.projects.ph_description',
              'e.g. brand + growth spend',
            )}
            onChange={(e) =>
              setForm((f) => ({ ...f, description: e.target.value }))
            }
          />
        </label>

        <div className='muted' style={{ fontSize: 11 }}>
          {tr(
            'console.projects.modal_hint',
            'A project is a cost label for reporting. It grants no access and has no members — assign tokens to it on the Tokens page.',
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
          <button
            type='button'
            className='btn ghost'
            disabled={saving}
            onClick={onClose}
          >
            {tr('console.common.cancel', 'cancel')}
          </button>
          <button
            type='submit'
            className='btn primary'
            disabled={saving || !form.name.trim()}
            data-testid='proj-save'
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

export default ProjectModal;
