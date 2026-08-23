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
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { API, showSuccess } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import { quotaToUSD } from '../../../helpers/formatting';

/*
 * Cost-attribution projects (migration 029) — the tenant → project → token
 * dimension. CRUD over
 *   GET/POST        /api/v2/:tenant_slug/projects       (?include_deleted=1)
 *   GET/PUT/DELETE  /api/v2/:tenant_slug/projects/:id
 *   POST            /api/v2/:tenant_slug/projects/:id/restore
 *   GET             /api/v2/:tenant_slug/projects/spend
 *
 * A project is a LABEL, not a permission boundary: it carries no members and
 * grants nothing. Writes are tenant-admin gated (403 → the read-only state
 * below); reads are open to every user in the tenant because the Token page's
 * project picker has to work for ordinary members.
 *
 * NOTHING HERE IS A ONE-WAY DOOR. Delete is a soft delete that hands back the
 * ids of the tokens it detached; the banner below turns that into a one-click
 * undo, and retired projects stay listed with a restore button long after the
 * banner is gone. Every mutation is safe to trigger twice — the server treats a
 * repeat as "the state you asked for already holds" rather than an error, and
 * in-flight buttons are disabled so a double-click cannot fire two requests.
 *
 * The spend table always shows an "unassigned" row (project_id 0) so the rows
 * add up to the tenant's total — a report whose parts don't sum to the whole
 * is one finance stops trusting immediately. Retired projects keep their spend
 * and their name for the same reason.
 */

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

const cellStyle = {
  padding: '10px 16px',
  borderBottom: '1px dashed var(--hf-rule)',
  fontSize: 12,
};

// USD display via the shared helper: quotaToUSD divides by the operator's
// live quota_per_unit (getQuotaPerUSD), never the hardcoded default. A `$0.00`
// row is still a row — the spend table's parts must sum to the whole.
const fmtUSD = (quota) => `$${quotaToUSD(quota)}`;

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

// ─── Main page ────────────────────────────────────────────────────────────────

const HFProjects = () => {
  const { t: tr } = useTranslation();
  const tenantSlug = useTenantSlug();
  const [rows, setRows] = useState([]);
  const [spend, setSpend] = useState([]);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [editTarget, setEditTarget] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  // The undo offer for the most recent delete: the project plus the token ids
  // the server detached. Nothing else records which tokens were attached, so
  // this is the only way to put them back with one click.
  const [undo, setUndo] = useState(null);
  // Ids currently being restored — one entry per row, so a slow restore only
  // disables its own button.
  const [restoring, setRestoring] = useState([]);
  const busy = useRef(new Set());

  // Guard against double-firing the same action. Returns false when the action
  // is already in flight.
  const claim = useCallback((key) => {
    if (busy.current.has(key)) return false;
    busy.current.add(key);
    return true;
  }, []);
  const release = useCallback((key) => {
    busy.current.delete(key);
  }, []);

  const fetchAll = useCallback(async () => {
    if (!tenantSlug) return;
    setLoading(true);
    try {
      // include_deleted so retired projects stay visible (and restorable)
      // instead of vanishing the moment someone mis-clicks delete.
      const res = await API.get(
        `/api/v2/${tenantSlug}/projects?include_deleted=1`,
      );
      if (res?.data?.success) {
        setRows(res.data.data?.items ?? []);
        setForbidden(false);
      }
    } catch (err) {
      if (err?.response?.status === 403) setForbidden(true);
      setRows([]);
    } finally {
      setLoading(false);
    }
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/projects/spend`);
      if (res?.data?.success) setSpend(res.data.data?.items ?? []);
    } catch (_) {
      // The spend report failing must not take the CRUD list with it — they
      // are independent reads and the list is the actionable one.
      setSpend([]);
    }
  }, [tenantSlug]);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  const doDelete = async () => {
    if (!deleteTarget) return;
    const key = `delete:${deleteTarget.id}`;
    if (!claim(key)) return;
    try {
      const res = await API.delete(
        `/api/v2/${tenantSlug}/projects/${deleteTarget.id}`,
      );
      if (res?.data?.success) {
        showSuccess(tr('console.projects.toast_deleted', 'Project deleted'));
        // Keep everything the undo needs BEFORE clearing the target.
        setUndo({
          id: deleteTarget.id,
          name: deleteTarget.name,
          tokenIds: res.data.data?.detached_token_ids ?? [],
        });
        setDeleteTarget(null);
        await fetchAll();
      }
    } catch (_) {
      // error toast from the interceptor; the dialog stays open so the user
      // can retry or cancel deliberately.
    } finally {
      release(key);
    }
  };

  // restore is both the banner's undo and the retired-row button. Passing the
  // detached token ids puts the tokens back too; omitting them (the row button,
  // where that context is long gone) restores the project alone.
  const doRestore = async (id, tokenIds) => {
    const key = `restore:${id}`;
    if (!claim(key)) return;
    setRestoring((r) => (r.includes(id) ? r : [...r, id]));
    try {
      const res = await API.post(
        `/api/v2/${tenantSlug}/projects/${id}/restore`,
        { reattach_token_ids: tokenIds ?? [] },
      );
      if (res?.data?.success) {
        showSuccess(tr('console.projects.toast_restored', 'Project restored'));
        setUndo((u) => (u && u.id === id ? null : u));
        await fetchAll();
      }
    } catch (_) {
      // 409 = a live project has taken the name; the interceptor surfaces the
      // server's message, which tells the user to rename one of them.
    } finally {
      release(key);
      setRestoring((r) => r.filter((x) => x !== id));
    }
  };

  const handleSaved = async () => {
    setEditTarget(null);
    await fetchAll();
  };

  const liveRows = useMemo(() => rows.filter((r) => !r.deleted), [rows]);
  const deletedRows = useMemo(() => rows.filter((r) => r.deleted), [rows]);

  const spendTotal = useMemo(
    () => spend.reduce((acc, r) => acc + (r.total_quota || 0), 0),
    [spend],
  );

  const spendLabel = (r) =>
    r.unassigned
      ? tr('console.projects.unassigned', 'unassigned')
      : r.name || `#${r.project_id}`;

  return (
    <HFShell
      active='projects'
      crumbs={[
        tr('console.nav.section_platform_admin', 'platform · admin'),
        tr('console.projects.crumb', 'projects'),
      ]}
      actions={
        !forbidden && (
          <button
            type='button'
            className='btn primary'
            data-testid='proj-new-btn'
            onClick={() => setEditTarget({ isNew: true })}
          >
            {tr('console.projects.new', '+ new project')}
          </button>
        )
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.projects.crumb', 'projects')}
          </div>
          <h1>
            {loading
              ? '…'
              : tr('console.projects.count', '{{count}} projects', {
                  count: liveRows.length,
                })}
          </h1>
          <div className='sub'>
            {tr(
              'console.projects.sub',
              'cost attribution between tenant and token · a label, not an access boundary',
            )}
          </div>
        </div>
      </div>

      <div
        style={{
          padding: 24,
          display: 'flex',
          flexDirection: 'column',
          gap: 24,
        }}
      >
        {/* ── undo banner ──────────────────────────────────────────────────── */}
        {undo && (
          <div
            className='panel'
            data-testid='proj-undo-banner'
            style={{
              padding: '12px 16px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 12,
            }}
          >
            <span style={{ fontSize: 12 }}>
              {tr(
                'console.projects.undo_body',
                'Deleted "{{name}}". {{count}} token(s) were unassigned.',
                { name: undo.name, count: undo.tokenIds.length },
              )}
            </span>
            <span style={{ display: 'flex', gap: 8, whiteSpace: 'nowrap' }}>
              <button
                type='button'
                className='btn primary sm'
                data-testid='proj-undo-btn'
                disabled={restoring.includes(undo.id)}
                onClick={() => doRestore(undo.id, undo.tokenIds)}
              >
                {tr('console.projects.undo', 'undo')}
              </button>
              <button
                type='button'
                className='btn ghost sm'
                data-testid='proj-undo-dismiss'
                onClick={() => setUndo(null)}
              >
                {tr('console.common.dismiss', 'dismiss')}
              </button>
            </span>
          </div>
        )}

        {/* ── spend by project ─────────────────────────────────────────────── */}
        <div className='panel'>
          <div
            className='lbl'
            style={{
              padding: '12px 16px',
              borderBottom: '1px solid var(--hf-rule)',
            }}
          >
            {tr('console.projects.spend_title', 'spend by project')}
          </div>
          {spend.length === 0 ? (
            <div
              className='muted'
              style={{ padding: '20px 24px', fontSize: 12 }}
              data-testid='proj-spend-empty'
            >
              {tr('console.projects.spend_empty', 'No usage recorded yet.')}
            </div>
          ) : (
            <table
              className='hf-table'
              style={{ width: '100%', borderCollapse: 'collapse' }}
              data-testid='proj-spend-table'
            >
              <thead>
                <tr>
                  {[
                    tr('console.projects.col_project', 'project'),
                    tr('console.projects.col_requests', 'requests'),
                    tr('console.projects.col_spend', 'spend'),
                  ].map((h, i) => (
                    <th
                      key={i}
                      className='lbl'
                      style={{
                        textAlign: i === 0 ? 'left' : 'right',
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
                {spend.map((r) => (
                  <tr
                    key={r.project_id}
                    data-testid={`proj-spend-row-${r.project_id}`}
                  >
                    <td
                      className={r.unassigned ? 'muted' : 'strong'}
                      style={cellStyle}
                    >
                      {spendLabel(r)}
                    </td>
                    <td
                      className='mono'
                      style={{ ...cellStyle, textAlign: 'right' }}
                    >
                      {r.count}
                    </td>
                    <td
                      className='mono'
                      style={{ ...cellStyle, textAlign: 'right' }}
                    >
                      {fmtUSD(r.total_quota)}
                    </td>
                  </tr>
                ))}
                {/* The total is rendered from the SAME rows the table shows, so
                    "the parts add up to the whole" is visible, not asserted. */}
                <tr data-testid='proj-spend-total'>
                  <td
                    className='lbl'
                    style={{ ...cellStyle, borderBottom: 'none' }}
                  >
                    {tr('console.projects.spend_total', 'total')}
                  </td>
                  <td style={{ ...cellStyle, borderBottom: 'none' }} />
                  <td
                    className='mono strong'
                    style={{
                      ...cellStyle,
                      borderBottom: 'none',
                      textAlign: 'right',
                    }}
                  >
                    {fmtUSD(spendTotal)}
                  </td>
                </tr>
              </tbody>
            </table>
          )}
        </div>

        {/* ── project list ─────────────────────────────────────────────────── */}
        <div className='panel'>
          {loading ? (
            <div
              className='muted'
              style={{ padding: '20px 24px', fontSize: 12 }}
            >
              {tr('console.common.loading', 'Loading…')}
            </div>
          ) : forbidden ? (
            <div style={{ padding: '20px 24px' }} data-testid='proj-forbidden'>
              <div className='strong' style={{ marginBottom: 6 }}>
                {tr('console.projects.admin_required', 'Admin access required')}
              </div>
              <div className='muted' style={{ fontSize: 12 }}>
                {tr(
                  'console.projects.admin_required_body',
                  'You do not have permission to manage projects. Contact a tenant administrator.',
                )}
              </div>
            </div>
          ) : liveRows.length === 0 ? (
            <div
              className='muted'
              style={{ padding: '20px 24px', fontSize: 12 }}
              data-testid='proj-empty'
            >
              {tr(
                'console.projects.empty',
                'No projects yet. Create one, then assign tokens to it on the Tokens page.',
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
                    tr('console.projects.col_name', 'name'),
                    tr('console.projects.col_description', 'description'),
                    '',
                  ].map((h, i) => (
                    <th
                      key={i}
                      className='lbl'
                      style={{
                        textAlign: i === 2 ? 'right' : 'left',
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
                {liveRows.map((r) => (
                  <tr key={r.id} data-testid={`proj-row-${r.id}`}>
                    <td className='mono strong' style={cellStyle}>
                      {r.name}
                    </td>
                    <td className='muted' style={cellStyle}>
                      {r.description}
                    </td>
                    <td
                      style={{
                        ...cellStyle,
                        textAlign: 'right',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      <button
                        type='button'
                        className='btn ghost sm'
                        data-testid={`proj-edit-btn-${r.id}`}
                        onClick={() => setEditTarget({ ...r, isNew: false })}
                      >
                        {tr('console.common.edit', 'edit')}
                      </button>{' '}
                      <button
                        type='button'
                        className='btn ghost sm'
                        data-testid={`proj-delete-btn-${r.id}`}
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

        {/* ── retired projects ─────────────────────────────────────────────── */}
        {deletedRows.length > 0 && (
          <div className='panel' data-testid='proj-deleted-panel'>
            <div
              className='lbl'
              style={{
                padding: '12px 16px',
                borderBottom: '1px solid var(--hf-rule)',
              }}
            >
              {tr('console.projects.deleted_title', 'retired projects')}
            </div>
            <div
              className='muted'
              style={{ padding: '10px 16px', fontSize: 11 }}
            >
              {tr(
                'console.projects.deleted_hint',
                'Kept so past usage still reports under a readable name — and so a delete is never final. Restoring does not re-attach tokens unless you use the undo right after deleting.',
              )}
            </div>
            <table
              className='hf-table'
              style={{ width: '100%', borderCollapse: 'collapse' }}
            >
              <tbody>
                {deletedRows.map((r) => (
                  <tr key={r.id} data-testid={`proj-deleted-row-${r.id}`}>
                    <td className='mono muted' style={cellStyle}>
                      {r.name}
                    </td>
                    <td
                      style={{
                        ...cellStyle,
                        textAlign: 'right',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      <button
                        type='button'
                        className='btn ghost sm'
                        data-testid={`proj-restore-btn-${r.id}`}
                        disabled={restoring.includes(r.id)}
                        onClick={() => doRestore(r.id, [])}
                      >
                        {restoring.includes(r.id)
                          ? tr('console.common.loading', 'loading…')
                          : tr('console.projects.restore', 'restore')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {editTarget && (
        <ProjectModal
          tenantSlug={tenantSlug}
          target={editTarget}
          onSaved={handleSaved}
          onClose={() => setEditTarget(null)}
        />
      )}

      <ConfirmDialog
        visible={!!deleteTarget}
        title={tr(
          'console.projects.delete_confirm',
          'Delete project "{{name}}"? Its tokens become unassigned; past usage keeps this project in reports.',
          { name: deleteTarget?.name || '' },
        )}
        consequenceList={[
          tr(
            'console.projects.delete_reversible',
            'Reversible: the project is retired, not erased, and you can undo it right after.',
          ),
        ]}
        confirmText={deleteTarget?.name || ''}
        confirmButtonText={tr('console.common.delete', 'delete')}
        onConfirm={doDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </HFShell>
  );
};

export default HFProjects;
