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
import { getQuotaPerUSD } from '../../../../helpers/formatting';

/*
 * v2 admin — user management. Wired to /api/v2/admin/users (RootJWTAuth, session
 * fallback → works with the console session like the Tenants page). The list is
 * a field-whitelisted projection; this page never receives password/token/2FA
 * fields. Create is deferred (needs a password/invite flow) → greyed control.
 */

const quotaToUSD = (q) => ((q || 0) / getQuotaPerUSD()).toFixed(2);

// role ints mirror common.Role* (1 user, 5 subscriber, 10 admin, 100 root).
// Labels are [key, fallback] pairs resolved at render via tr() — module scope
// has no i18n context.
const ROLE_OPTIONS = [
  [1, 'role_user', 'user'],
  [5, 'role_subscriber', 'subscriber'],
  [10, 'role_admin', 'admin'],
  [100, 'role_root', 'root'],
];
const roleLabel = (tr, r) => {
  const opt = ROLE_OPTIONS.find(([v]) => v === r);
  return opt ? tr(`console.admin.users.${opt[1]}`, opt[2]) : `#${r}`;
};

const STATUS_OPTIONS = [
  [1, 'status_enabled', 'enabled'],
  [2, 'status_disabled', 'disabled'],
];
const statusLabel = (tr, s) =>
  s === 1
    ? tr('console.admin.users.status_enabled', 'enabled')
    : s === 2
      ? tr('console.admin.users.status_disabled', 'disabled')
      : '—';
const statusClass = (s) => (s === 1 ? 'tag ok' : 'tag');

// ─── Edit modal ───────────────────────────────────────────────────────────────

const EditModal = ({ user, onSaved, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({
    role: user.role,
    status: user.status,
    quotaUSD: quotaToUSD(user.quota),
    group: user.group || 'default',
  });
  const [saving, setSaving] = useState(false);

  const submit = async (e) => {
    e.preventDefault();
    setSaving(true);
    try {
      const body = {
        role: Number(form.role),
        status: Number(form.status),
        quota: Math.max(
          0,
          Math.round((parseFloat(form.quotaUSD) || 0) * getQuotaPerUSD()),
        ),
        group: form.group.trim() || 'default',
      };
      const res = await API.put(`/api/v2/admin/users/${user.id}`, body);
      if (res?.data?.success) {
        showSuccess(tr('console.admin.users.toast_updated', 'User updated'));
        onSaved();
      }
    } catch (_) {
      // error toast shown by API interceptor
    } finally {
      setSaving(false);
    }
  };

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
          {tr('console.admin.users.edit_title', 'Edit · {{name}}', {
            name: user.username,
          })}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.admin.users.field_role', 'role')}
          </span>
          <select
            data-testid='edit-role'
            style={{ ...inputStyle, cursor: 'pointer' }}
            value={form.role}
            onChange={(e) => setForm((f) => ({ ...f, role: e.target.value }))}
          >
            {ROLE_OPTIONS.map(([v, k, l]) => (
              <option key={v} value={v}>
                {tr(`console.admin.users.${k}`, l)}
              </option>
            ))}
          </select>
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.admin.users.field_status', 'status')}
          </span>
          <select
            data-testid='edit-status'
            style={{ ...inputStyle, cursor: 'pointer' }}
            value={form.status}
            onChange={(e) => setForm((f) => ({ ...f, status: e.target.value }))}
          >
            {STATUS_OPTIONS.map(([v, k, l]) => (
              <option key={v} value={v}>
                {tr(`console.admin.users.${k}`, l)}
              </option>
            ))}
          </select>
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.admin.users.field_quota_cap', 'quota cap ($)')}
          </span>
          <input
            data-testid='edit-quota'
            style={inputStyle}
            type='number'
            min='0'
            step='0.01'
            value={form.quotaUSD}
            onChange={(e) =>
              setForm((f) => ({ ...f, quotaUSD: e.target.value }))
            }
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.admin.users.field_group', 'group')}
          </span>
          <input
            data-testid='edit-group'
            style={inputStyle}
            value={form.group}
            onChange={(e) => setForm((f) => ({ ...f, group: e.target.value }))}
            placeholder='default'
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
            data-testid='edit-save'
            disabled={saving}
          >
            {saving
              ? tr('console.admin.users.saving', 'saving…')
              : tr('console.admin.users.save_changes', 'save changes')}
          </button>
        </div>
      </form>
    </div>
  );
};

// ─── Main page ────────────────────────────────────────────────────────────────

const HFAdminUsers = () => {
  // Aliased to `tr` to match the v2 console convention (avoids shadowing in
  // callbacks that use `t` as a loop variable elsewhere).
  const { t: tr } = useTranslation();
  const [users, setUsers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [editing, setEditing] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [actioning, setActioning] = useState(false);
  const searchRef = useRef(null);

  const fetchUsers = useCallback(async (kw = '', status = '') => {
    setLoading(true);
    setForbidden(false);
    try {
      const params = new URLSearchParams({ page: '1', page_size: '50' });
      if (kw.trim()) params.set('keyword', kw.trim());
      if (status) params.set('status', status);
      const res = await API.get(`/api/v2/admin/users?${params.toString()}`, {
        skipErrorHandler: true,
      });
      if (res?.data?.success) {
        setUsers(res.data.data.users ?? []);
      }
    } catch (err) {
      if (err?.response?.status === 403) setForbidden(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchUsers();
  }, [fetchUsers]);

  // Debounced keyword search.
  useEffect(() => {
    const id = setTimeout(() => fetchUsers(keyword, statusFilter), 350);
    return () => clearTimeout(id);
  }, [keyword, statusFilter, fetchUsers]);

  const handleSaved = async () => {
    setEditing(null);
    await fetchUsers(keyword, statusFilter);
  };

  const performDelete = async () => {
    if (!deleting) return;
    setActioning(true);
    try {
      const res = await API.delete(`/api/v2/admin/users/${deleting.id}`);
      if (res?.data?.success) {
        showSuccess(tr('console.admin.users.toast_deleted', 'User deleted'));
        setDeleting(null);
        await fetchUsers(keyword, statusFilter);
      }
    } catch (_) {
      // error toast from interceptor
    } finally {
      setActioning(false);
    }
  };

  const inputStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 12,
    padding: '6px 10px',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
  };

  return (
    <HFShell
      active='admin-users'
      crumbs={[
        tr('console.admin.users.crumb_admin', 'platform · admin'),
        tr('console.admin.users.crumb', 'users'),
      ]}
      actions={
        <>
          {!loading && !forbidden && (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.admin.users.count', { count: users.length })}
            </span>
          )}
          {/* Create is deferred — needs a password/invite flow. Honest greyed
              control rather than a button that 404s. */}
          <button
            type='button'
            className='btn primary'
            data-testid='new-user-btn'
            disabled
            title={tr(
              'console.admin.users.new_user_deferred',
              'creating users needs a password/invite flow — deferred',
            )}
          >
            {tr('console.admin.users.new_user', '+ new user')}
          </button>
        </>
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.users.heading_lbl', 'users')}
          </div>
          <h1>
            {loading
              ? '…'
              : forbidden
                ? tr(
                    'console.admin.users.forbidden_title',
                    'Admin access required',
                  )
                : tr('console.admin.users.count', { count: users.length })}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.users.sub',
              'role · status · quota · per-user management',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.users.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.users.forbidden_body',
                'You do not have permission to manage users. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          {/* Filters */}
          <div style={{ marginBottom: 16, display: 'flex', gap: 10 }}>
            <input
              ref={searchRef}
              data-testid='user-search'
              style={{ ...inputStyle, width: 260 }}
              placeholder={tr('console.admin.users.ph_search', 'search users…')}
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
            <select
              data-testid='user-status-filter'
              style={{ ...inputStyle, cursor: 'pointer' }}
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value)}
            >
              <option value=''>
                {tr('console.admin.users.all_status', 'all status')}
              </option>
              <option value='1'>
                {tr('console.admin.users.status_enabled', 'enabled')}
              </option>
              <option value='2'>
                {tr('console.admin.users.status_disabled', 'disabled')}
              </option>
            </select>
          </div>

          <div className='panel'>
            {loading ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : users.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {keyword || statusFilter
                  ? tr(
                      'console.admin.users.empty_filtered',
                      'No users match your search.',
                    )
                  : tr('console.admin.users.empty', 'No users yet.')}
              </div>
            ) : (
              <div className='hf-table-scroll'>
                <table className='t'>
                  <thead>
                    <tr>
                      <th>{tr('console.admin.users.th_user', 'user')}</th>
                      <th>{tr('console.admin.users.th_role', 'role')}</th>
                      <th>{tr('console.admin.users.th_status', 'status')}</th>
                      <th>{tr('console.admin.users.th_group', 'group')}</th>
                      <th>
                        {tr('console.admin.users.th_quota', 'quota used · cap')}
                      </th>
                      <th>
                        {tr('console.admin.users.th_requests', 'requests')}
                      </th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {users.map((u) => (
                      <tr key={u.id} data-testid={`user-row-${u.id}`}>
                        <td>
                          <div className='strong'>
                            {u.display_name || u.username}
                          </div>
                          <div className='faint mono' style={{ fontSize: 10 }}>
                            {u.email || u.username}
                          </div>
                        </td>
                        <td>
                          <span className='tag'>{roleLabel(tr, u.role)}</span>
                        </td>
                        <td>
                          <span
                            data-testid={`user-status-${u.id}`}
                            className={statusClass(u.status)}
                          >
                            {statusLabel(tr, u.status)}
                          </span>
                        </td>
                        <td className='mono muted'>{u.group || 'default'}</td>
                        <td className='mono'>
                          ${quotaToUSD(u.used_quota)} / ${quotaToUSD(u.quota)}
                        </td>
                        <td className='mono muted'>{u.request_count ?? 0}</td>
                        <td>
                          <div style={{ display: 'flex', gap: 6 }}>
                            <button
                              type='button'
                              className='btn ghost sm'
                              data-testid={`user-edit-btn-${u.id}`}
                              onClick={() => setEditing(u)}
                            >
                              {tr('console.common.edit', 'edit')}
                            </button>
                            <button
                              type='button'
                              className='btn ghost sm'
                              data-testid={`user-delete-btn-${u.id}`}
                              style={{ color: 'var(--hf-err)' }}
                              onClick={() => setDeleting(u)}
                            >
                              {tr('console.common.delete', 'delete')}
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}

      {editing && (
        <EditModal
          user={editing}
          onSaved={handleSaved}
          onClose={() => setEditing(null)}
        />
      )}

      <ConfirmDialog
        visible={!!deleting}
        title={tr(
          'console.admin.users.delete_title',
          'Delete user "{{name}}"?',
          {
            name: deleting?.username || '',
          },
        )}
        consequenceList={[
          tr(
            'console.admin.users.delete_warn_access',
            'The user loses access immediately',
          ),
          tr(
            'console.admin.users.delete_warn_undo',
            'This action cannot be undone',
          ),
        ]}
        confirmText={deleting?.username || ''}
        onConfirm={performDelete}
        onCancel={() => !actioning && setDeleting(null)}
      />
    </HFShell>
  );
};

export default HFAdminUsers;
