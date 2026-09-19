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
import { API, showError, showSuccess } from '../../../helpers';
import { inviteLink } from '../../../helpers/inviteLink';

// ─────────────────────────────────────────────────────────────────────────────
// Pure derivation helpers — exported for vitest coverage.

/**
 * The delivery URL an operator sends the invitee. Re-exported here (rather
 * than defined here) because OidcRedirect.jsx uses the same helper for
 * return_to after the identity round trip — the issue link and the
 * carry-back link are built by one function, so they cannot drift.
 */
export { inviteLink };

/** Status 1=pending(usable) / 2=consumed / 3=revoked (entity/tenant_invite.go). */
export const inviteStatusLabel = (status) => {
  if (status === 1) return 'pending';
  if (status === 2) return 'consumed';
  if (status === 3) return 'revoked';
  return 'unknown';
};

/** True when a row's revoke action should be offered — only pending codes. */
export const isRevocable = (invite) => !!invite && invite.status === 1;

// ─────────────────────────────────────────────────────────────────────────────
// Drawer component

const overlayStyle = {
  position: 'fixed',
  inset: 0,
  background: 'rgba(0,0,0,0.45)',
  zIndex: 500,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
};

const panelStyle = {
  background: 'var(--hf-paper)',
  border: '1px solid var(--hf-rule)',
  borderRadius: 4,
  padding: 28,
  width: 640,
  maxHeight: '90vh',
  overflowY: 'auto',
  display: 'flex',
  flexDirection: 'column',
  gap: 16,
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

const statusTagClass = (status) => {
  if (status === 1) return 'tag';
  if (status === 2) return 'tag ok';
  if (status === 3) return 'tag warn';
  return 'tag';
};

const InvitesDrawer = ({ tenantId, tenantName, onClose }) => {
  const { t } = useTranslation();
  const [invites, setInvites] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [ttlHours, setTtlHours] = useState('72');
  const [issuing, setIssuing] = useState(false);
  // The plaintext link is held ONLY here, client-side, for the lifetime of
  // this drawer session — the list endpoint this drawer also reads from
  // never returns it again (handler.ListTenantInvites projects code_prefix).
  const [issuedLink, setIssuedLink] = useState(null);
  const [revoking, setRevoking] = useState(null);

  const reload = useCallback(async () => {
    setLoading(true);
    setLoadFailed(false);
    try {
      const res = await API.get(`/api/v2/admin/tenants/${tenantId}/invites`);
      if (res?.data?.success) {
        setInvites(res.data.data?.invites || []);
      } else {
        setInvites([]);
        setLoadFailed(true);
      }
    } catch (_) {
      // error toast shown by API interceptor; still track the failure here
      // so the panel doesn't render the empty-state text as if it knows
      // there are zero invites.
      setInvites([]);
      setLoadFailed(true);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    reload();
  }, [reload]);

  const submitIssue = async (e) => {
    e.preventDefault();
    setIssuing(true);
    try {
      const hours = Number(ttlHours);
      const body = hours > 0 ? { ttl_hours: hours } : {};
      const res = await API.post(
        `/api/v2/admin/tenants/${tenantId}/invites`,
        body,
      );
      if (res?.data?.success) {
        setIssuedLink(inviteLink(window.location.origin, res.data.data.code));
        showSuccess(t('console.tenant.toast_invite_issued', 'Invite issued'));
        await reload();
      } else {
        showError(
          res?.data?.message ||
            t(
              'console.tenant.toast_invite_issue_failed',
              'Failed to issue invite',
            ),
        );
      }
    } catch (err) {
      showError(
        err?.response?.data?.message ||
          err?.message ||
          t(
            'console.tenant.toast_invite_issue_failed',
            'Failed to issue invite',
          ),
      );
    } finally {
      setIssuing(false);
    }
  };

  const copyLink = async () => {
    if (!issuedLink) return;
    try {
      await navigator.clipboard.writeText(issuedLink);
      showSuccess(t('console.common.copied', 'copied'));
    } catch (_) {
      showError(t('console.common.copy_failed', 'copy failed'));
    }
  };

  const revoke = async (invite) => {
    setRevoking(invite.id);
    try {
      const res = await API.delete(
        `/api/v2/admin/tenants/${tenantId}/invites/${invite.id}`,
      );
      if (res?.data?.success) {
        showSuccess(t('console.tenant.toast_invite_revoked', 'Invite revoked'));
        await reload();
      }
    } catch (err) {
      showError(
        err?.response?.data?.message ||
          err?.message ||
          t(
            'console.tenant.toast_invite_revoke_failed',
            'Failed to revoke invite',
          ),
      );
    } finally {
      setRevoking(null);
    }
  };

  return (
    <div
      style={overlayStyle}
      onClick={(e) => e.target === e.currentTarget && onClose()}
      data-testid='invites-overlay'
    >
      <div style={panelStyle}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <div className='strong' style={{ fontSize: 15 }}>
            {tenantName || tenantId} ·{' '}
            {t('console.tenant.invite_drawer_title', 'invites')}
          </div>
          <button
            type='button'
            className='btn ghost sm'
            onClick={onClose}
            aria-label={t('console.common.close', 'close')}
          >
            ✕
          </button>
        </div>

        <form
          onSubmit={submitIssue}
          style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
          data-testid='invite-issue-form'
        >
          <div className='strong' style={{ fontSize: 13 }}>
            {t('console.tenant.invite_issue_section', 'issue invite')}
          </div>
          <label className='lbl'>
            {t('console.tenant.field_ttl_hours', 'ttl (hours)')}
          </label>
          <input
            type='number'
            min='0'
            step='1'
            value={ttlHours}
            onChange={(e) => setTtlHours(e.target.value)}
            placeholder={t(
              'console.tenant.ph_ttl_hours',
              '72 = 3 days, 0 = never expires',
            )}
            style={inputStyle}
            data-testid='invite-ttl-hours'
          />
          <button
            type='submit'
            className='btn primary'
            disabled={issuing}
            data-testid='invite-issue-submit'
          >
            {issuing
              ? t('console.tenant.invite_issuing', 'issuing…')
              : t('console.tenant.invite_issue_submit', 'issue invite')}
          </button>
        </form>

        {issuedLink && (
          <div
            className='panel'
            style={{ padding: 14 }}
            data-testid='invite-issued-box'
          >
            <div className='lbl' style={{ marginBottom: 4 }}>
              {t('console.tenant.invite_link_label', 'one-time invite link')}
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <input
                type='text'
                readOnly
                value={issuedLink}
                style={{ ...inputStyle, flex: 1 }}
                data-testid='invite-issued-link'
                onFocus={(e) => e.target.select()}
              />
              <button
                type='button'
                className='btn ghost sm'
                onClick={copyLink}
                data-testid='invite-issued-copy'
              >
                {t('console.common.copy', 'copy')}
              </button>
            </div>
            <div className='muted' style={{ fontSize: 11, marginTop: 6 }}>
              {t(
                'console.tenant.invite_issued_notice',
                'Shown once — copy this link now. It will not be shown again.',
              )}
            </div>
          </div>
        )}

        <div data-testid='invite-list'>
          <div className='strong' style={{ fontSize: 13, marginBottom: 6 }}>
            {t('console.tenant.invite_list_title', 'issued invites')}
          </div>
          {loading ? (
            <div className='muted' style={{ fontSize: 12 }}>
              {t('console.common.loading', 'loading…')}
            </div>
          ) : loadFailed ? (
            <div
              className='muted'
              style={{ fontSize: 12 }}
              data-testid='invite-list-load-failed'
            >
              {t(
                'console.tenant.invite_list_load_failed',
                'Could not load invites.',
              )}
            </div>
          ) : invites.length === 0 ? (
            <div className='muted' style={{ fontSize: 12 }}>
              {t('console.tenant.empty_invites', 'No invites issued yet.')}
            </div>
          ) : (
            <table className='table sm' style={{ width: '100%', fontSize: 12 }}>
              <thead>
                <tr>
                  <th>{t('console.tenant.th_invite_code', 'code')}</th>
                  <th>{t('console.tenant.th_invite_status', 'status')}</th>
                  <th>{t('console.tenant.th_invite_expiry', 'expires')}</th>
                  <th>
                    {t('console.tenant.th_invite_consumer', 'consumed by')}
                  </th>
                  <th>{t('console.tenant.th_invite_created', 'created')}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {invites.map((inv) => (
                  <tr key={inv.id} data-testid={`invite-row-${inv.id}`}>
                    <td className='mono'>{inv.code_prefix}…</td>
                    <td>
                      <span className={statusTagClass(inv.status)}>
                        {t(
                          `console.tenant.invite_status_${inviteStatusLabel(inv.status)}`,
                          inviteStatusLabel(inv.status),
                        )}
                      </span>
                    </td>
                    <td className='mono'>
                      {inv.expired_time
                        ? new Date(inv.expired_time * 1000).toLocaleString()
                        : t('console.tenant.invite_never_expires', 'never')}
                    </td>
                    <td className='mono'>
                      {inv.consumed_by_account_id ??
                        t('console.tenant.invite_not_consumed', '—')}
                    </td>
                    <td className='mono'>
                      {new Date(inv.created_at).toLocaleString()}
                    </td>
                    <td>
                      {isRevocable(inv) && (
                        <button
                          type='button'
                          className='btn ghost sm'
                          disabled={revoking === inv.id}
                          onClick={() => revoke(inv)}
                          data-testid={`invite-revoke-btn-${inv.id}`}
                        >
                          {revoking === inv.id
                            ? '…'
                            : t('console.tenant.btn_revoke', 'revoke')}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
};

export default InvitesDrawer;
