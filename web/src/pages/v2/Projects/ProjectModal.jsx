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
import { getQuotaPerUSD, quotaToUSD } from '../../../helpers/formatting';
import HfDialog, { HfDialogFooter } from '../../../components/hifi/HfDialog';

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
    // Edited in USD; stored as quota (monthly_budget_quota). Empty or 0 =
    // no cap. quotaToUSD renders the stored figure at the operator's live
    // quota_per_unit so the field shows what the admin typed last time.
    budgetUSD:
      target.monthly_budget_quota > 0
        ? quotaToUSD(target.monthly_budget_quota)
        : '',
  });
  const [saving, setSaving] = useState(false);
  // A ref, not the `saving` state: two submits fired in the same tick (Enter
  // plus a click) would both read the pre-render value of the state.
  const inFlight = useRef(false);
  // Handed to the dialog rather than `autoFocus`: an autoFocus child takes
  // focus during commit, before the dialog records who opened it, so focus
  // would have nowhere to return on close.
  const nameRef = useRef(null);

  const submit = async (e) => {
    e.preventDefault();
    const name = form.name.trim();
    if (!name || inFlight.current) return;
    inFlight.current = true;
    setSaving(true);
    try {
      const budgetUSD = parseFloat(form.budgetUSD);
      const payload = {
        name,
        description: form.description.trim(),
        // Always sent, so clearing the field clears the cap on the server
        // (an omitted field means "leave the cap alone" there).
        monthly_budget_quota:
          Number.isFinite(budgetUSD) && budgetUSD > 0
            ? Math.round(budgetUSD * getQuotaPerUSD())
            : 0,
      };
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
    <HfDialog
      title={
        target.isNew
          ? tr('console.projects.modal_new', 'New project')
          : tr('console.projects.modal_edit', 'Edit project')
      }
      onClose={onClose}
      as='form'
      onSubmit={submit}
      busy={saving}
      initialFocusRef={nameRef}
    >
      <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <span className='lbl'>
          {tr('console.projects.field_name', 'name *')}
        </span>
        <input
          data-testid='proj-name'
          ref={nameRef}
          style={inputStyle}
          value={form.name}
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

      <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <span className='lbl'>
          {tr('console.projects.field_budget', 'monthly budget (USD)')}
        </span>
        <input
          data-testid='proj-budget'
          style={inputStyle}
          type='number'
          min='0'
          step='0.01'
          inputMode='decimal'
          value={form.budgetUSD}
          disabled={saving}
          placeholder={tr('console.projects.ph_budget', 'empty = no cap')}
          onChange={(e) =>
            setForm((f) => ({ ...f, budgetUSD: e.target.value }))
          }
        />
        <span className='muted' style={{ fontSize: 11 }}>
          {tr(
            'console.projects.budget_hint',
            'Requests from tokens on this project are refused (402 project_budget_exceeded) once its spend this UTC month would cross the cap.',
          )}
        </span>
      </label>

      <div className='muted' style={{ fontSize: 11 }}>
        {tr(
          'console.projects.modal_hint',
          'A project is a cost label for reporting. It grants no access and has no members — assign tokens to it on the Tokens page.',
        )}
      </div>

      <HfDialogFooter>
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
      </HfDialogFooter>
    </HfDialog>
  );
};

export default ProjectModal;
