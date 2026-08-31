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
import NotAvailable from '../../../../components/hifi/NotAvailable';
import { API, showSuccess } from '../../../../helpers';

/*
 * v2 admin — system settings panels. Wired to /api/v2/admin/options
 * (GET filters secret-suffixed keys; PUT delegates per-key validation + audit).
 * This is a curated, high-value SUBSET — NOT New-API's full 2000-key page. The
 * long tail (ratio JSON, advanced relay tuning) is honestly noted as managed in
 * v1 / advanced rather than half-surfaced here. Each write is one key per call.
 */

// Field types: text | textarea | number | toggle. Each panel curates a subset.
// Labels are i18n [key, fallback] pairs (lk/lf) resolved at render via tr() —
// module scope has no i18n context.
const PANELS = [
  {
    id: 'general',
    lk: 'panel_general',
    lf: 'General',
    fields: [
      {
        key: 'SystemName',
        lk: 'f_system_name',
        lf: 'System name',
        type: 'text',
      },
      { key: 'Footer', lk: 'f_footer', lf: 'Footer', type: 'text' },
      { key: 'Notice', lk: 'f_notice', lf: 'Notice', type: 'textarea' },
      { key: 'About', lk: 'f_about', lf: 'About', type: 'textarea' },
    ],
  },
  {
    id: 'branding',
    lk: 'panel_branding',
    lf: 'Branding',
    fields: [
      { key: 'Logo', lk: 'f_logo', lf: 'Logo URL', type: 'text' },
      {
        key: 'HomePageContent',
        lk: 'f_home_page_content',
        lf: 'Home page content',
        type: 'textarea',
      },
      {
        key: 'console_setting.announcements',
        lk: 'f_announcements',
        lf: 'Announcements (JSON — validated)',
        type: 'textarea',
      },
      {
        key: 'console_setting.faq',
        lk: 'f_faq',
        lf: 'FAQ (JSON — validated)',
        type: 'textarea',
      },
    ],
  },
  {
    id: 'auth',
    lk: 'panel_auth',
    lf: 'Auth',
    fields: [
      {
        key: 'PasswordLoginEnabled',
        lk: 'f_password_login',
        lf: 'Password login',
        type: 'toggle',
      },
      {
        key: 'PasswordRegisterEnabled',
        lk: 'f_password_register',
        lf: 'Password registration',
        type: 'toggle',
      },
      {
        key: 'GitHubOAuthEnabled',
        lk: 'f_github_oauth',
        lf: 'GitHub OAuth',
        type: 'toggle',
      },
      {
        key: 'TelegramOAuthEnabled',
        lk: 'f_telegram_oauth',
        lf: 'Telegram OAuth',
        type: 'toggle',
      },
      {
        key: 'WeChatAuthEnabled',
        lk: 'f_wechat_login',
        lf: 'WeChat login',
        type: 'toggle',
      },
      {
        key: 'TurnstileCheckEnabled',
        lk: 'f_turnstile',
        lf: 'Turnstile check',
        type: 'toggle',
      },
    ],
  },
  {
    id: 'security',
    lk: 'panel_security',
    lf: 'Security',
    fields: [
      {
        key: 'SensitiveActionRequire2FA',
        lk: 'f_require_2fa',
        lf: 'Require 2FA for sensitive actions',
        type: 'toggle',
      },
      {
        key: 'SessionTimeoutMinutes',
        lk: 'f_session_timeout',
        lf: 'Session timeout (minutes)',
        type: 'number',
      },
      {
        key: 'EmailDomainRestrictionEnabled',
        lk: 'f_email_domain_restriction',
        lf: 'Email domain restriction',
        type: 'toggle',
      },
    ],
  },
  {
    id: 'quota',
    lk: 'panel_quota',
    lf: 'Quota',
    fields: [
      {
        key: 'QuotaForNewUser',
        lk: 'f_quota_new_user',
        lf: 'Quota for new user',
        type: 'number',
      },
      {
        key: 'QuotaForInviter',
        lk: 'f_quota_inviter',
        lf: 'Quota for inviter',
        type: 'number',
      },
      {
        key: 'USDExchangeRate',
        lk: 'f_usd_exchange_rate',
        lf: 'USD exchange rate',
        type: 'number',
      },
    ],
    // Ratio JSON editors (ModelRatio/GroupRatio) are large and validated
    // server-side — deferred here with an honest note rather than half-built.
    noteKey: 'note_quota',
    noteFallback:
      'Model/Group ratio JSON editors are large and live in v1 / advanced — not surfaced here. Validation lives server-side.',
  },
  {
    id: 'monitoring',
    lk: 'panel_monitoring',
    lf: 'Monitoring',
    readOnly: true,
  },
];

const inputStyle = {
  fontFamily: 'var(--hf-mono)',
  fontSize: 12,
  padding: '6px 8px',
  border: '1px solid var(--hf-rule)',
  background: 'var(--hf-sunken)',
  color: 'var(--hf-ink)',
  borderRadius: 2,
  outline: 'none',
  width: '100%',
};

const HFAdminSettings = () => {
  const { t: tr } = useTranslation();
  const [options, setOptions] = useState({});
  const [drafts, setDrafts] = useState({});
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [panel, setPanel] = useState('general');
  const [savingKey, setSavingKey] = useState(null);
  const [errors, setErrors] = useState({}); // key → message
  const [stats, setStats] = useState(null);
  const [statsLoading, setStatsLoading] = useState(false);

  const fetchOptions = useCallback(async () => {
    setLoading(true);
    setForbidden(false);
    try {
      const res = await API.get('/api/v2/admin/options', {
        skipErrorHandler: true,
      });
      if (res?.data?.success) {
        const map = {};
        (res.data.data ?? []).forEach((o) => {
          map[o.key] = o.value;
        });
        setOptions(map);
        setDrafts(map);
      }
    } catch (err) {
      if (err?.response?.status === 403) setForbidden(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchOptions();
  }, [fetchOptions]);

  // Monitoring is read-only — pull the system stats already wired at /admin/stats.
  useEffect(() => {
    if (panel !== 'monitoring') return;
    let cancelled = false;
    setStatsLoading(true);
    API.get('/api/v2/admin/stats', { skipErrorHandler: true })
      .then((res) => {
        if (!cancelled && res?.data?.success) setStats(res.data.data);
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setStatsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [panel]);

  // Persist one key per call. Surfaces the backend's honest validation-failure
  // message inline (UpdateOption returns 200 + success:false for those).
  const putOption = useCallback(
    async (key, value) => {
      setSavingKey(key);
      setErrors((prev) => ({ ...prev, [key]: undefined }));
      try {
        const res = await API.put(
          '/api/v2/admin/options',
          { key, value },
          { skipErrorHandler: true },
        );
        if (res?.data?.success) {
          setOptions((prev) => ({ ...prev, [key]: String(value) }));
          showSuccess(tr('console.admin.settings.toast_saved', 'Saved'));
          return true;
        }
        setErrors((prev) => ({
          ...prev,
          [key]:
            res?.data?.message ||
            tr('console.admin.settings.err_rejected', 'Update rejected'),
        }));
        return false;
      } catch (err) {
        setErrors((prev) => ({
          ...prev,
          [key]:
            err?.response?.data?.message ||
            tr('console.admin.settings.err_failed', 'Update failed'),
        }));
        return false;
      } finally {
        setSavingKey(null);
      }
    },
    [tr],
  );

  const isOn = (key) => options[key] === 'true';
  const dirty = (key) => (drafts[key] ?? '') !== (options[key] ?? '');

  const renderField = (f) => {
    const err = errors[f.key];
    const saving = savingKey === f.key;

    if (f.type === 'toggle') {
      return (
        <div
          key={f.key}
          style={{ marginBottom: 16 }}
          data-testid={`field-${f.key}`}
        >
          <label
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              cursor: 'pointer',
            }}
          >
            <input
              type='checkbox'
              data-testid={`toggle-${f.key}`}
              checked={isOn(f.key)}
              disabled={saving}
              onChange={() => putOption(f.key, !isOn(f.key))}
            />
            <span className='strong' style={{ fontSize: 13 }}>
              {tr(`console.admin.settings.${f.lk}`, f.lf)}
            </span>
          </label>
          {err && (
            <div
              data-testid={`error-${f.key}`}
              style={{ color: 'var(--hf-err)', fontSize: 11, marginTop: 4 }}
            >
              {err}
            </div>
          )}
        </div>
      );
    }

    const commonProps = {
      'data-testid': `field-${f.key}`,
      style: inputStyle,
      value: drafts[f.key] ?? '',
      onChange: (e) =>
        setDrafts((prev) => ({ ...prev, [f.key]: e.target.value })),
    };

    return (
      <div key={f.key} style={{ marginBottom: 18 }}>
        <div className='lbl' style={{ marginBottom: 5 }}>
          {tr(`console.admin.settings.${f.lk}`, f.lf)}
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
          {f.type === 'textarea' ? (
            <textarea
              rows={3}
              {...commonProps}
              style={{ ...inputStyle, resize: 'vertical' }}
            />
          ) : (
            <input
              type={f.type === 'number' ? 'number' : 'text'}
              {...commonProps}
            />
          )}
          <button
            type='button'
            className='btn primary sm'
            data-testid={`save-${f.key}`}
            disabled={saving || !dirty(f.key)}
            onClick={() => putOption(f.key, drafts[f.key] ?? '')}
          >
            {saving
              ? tr('console.admin.settings.saving', 'saving…')
              : tr('console.common.save', 'save')}
          </button>
        </div>
        {err && (
          <div
            data-testid={`error-${f.key}`}
            style={{ color: 'var(--hf-err)', fontSize: 11, marginTop: 4 }}
          >
            {err}
          </div>
        )}
      </div>
    );
  };

  const activePanel = PANELS.find((p) => p.id === panel) || PANELS[0];

  return (
    <HFShell
      active='admin-settings'
      crumbs={[
        tr('console.admin.settings.crumb_admin', 'operations & insights'),
        tr('console.admin.settings.crumb', 'settings'),
      ]}
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.settings.heading_lbl', 'admin settings')}
          </div>
          <h1>
            {forbidden
              ? tr(
                  'console.admin.settings.forbidden_title',
                  'Admin access required',
                )
              : tr('console.admin.settings.title', 'System settings')}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.settings.sub',
              'curated, high-value subset · one key per save',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.settings.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.settings.forbidden_body',
                'You do not have permission to manage system settings. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: '180px 1fr',
            minHeight: 0,
            height: '100%',
          }}
        >
          {/* Left nav */}
          <div
            style={{
              borderRight: '1px solid var(--hf-rule)',
              padding: '16px 0',
              background: 'var(--hf-paper)',
            }}
          >
            {PANELS.map((p) => (
              <button
                key={p.id}
                type='button'
                data-testid={`panel-${p.id}`}
                onClick={() => setPanel(p.id)}
                style={{
                  display: 'block',
                  width: '100%',
                  textAlign: 'left',
                  padding: '8px 20px',
                  border: 0,
                  background: panel === p.id ? 'var(--hf-elev)' : 'transparent',
                  borderLeft:
                    panel === p.id
                      ? '2px solid var(--hf-accent)'
                      : '2px solid transparent',
                  color: panel === p.id ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
                  cursor: 'pointer',
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 12,
                }}
              >
                {tr(`console.admin.settings.${p.lk}`, p.lf)}
              </button>
            ))}
          </div>

          {/* Panel body */}
          <div style={{ overflow: 'auto', padding: 28 }}>
            {loading ? (
              <div className='muted' style={{ fontSize: 12 }}>
                {tr('console.common.loading', 'loading…')}
              </div>
            ) : activePanel.readOnly ? (
              <div>
                <div className='lbl' style={{ marginBottom: 10 }}>
                  {tr(
                    'console.admin.settings.stats_lbl',
                    'system stats · read-only',
                  )}
                </div>
                {statsLoading ? (
                  <div className='muted' style={{ fontSize: 12 }}>
                    {tr('console.common.loading', 'loading…')}
                  </div>
                ) : stats ? (
                  <pre
                    data-testid='monitoring-stats'
                    className='mono'
                    style={{
                      margin: 0,
                      padding: 16,
                      fontSize: 11,
                      background: 'var(--hf-paper)',
                      border: '1px solid var(--hf-rule)',
                      color: 'var(--hf-ink-2)',
                      overflow: 'auto',
                    }}
                  >
                    {JSON.stringify(stats, null, 2)}
                  </pre>
                ) : (
                  <NotAvailable
                    reason={tr(
                      'console.admin.settings.stats_no_data',
                      'stats endpoint returned no data',
                    )}
                  />
                )}
              </div>
            ) : (
              <div style={{ maxWidth: 560 }}>
                <div className='lbl' style={{ marginBottom: 16 }}>
                  {tr(
                    `console.admin.settings.${activePanel.lk}`,
                    activePanel.lf,
                  ).toLowerCase()}
                </div>
                {activePanel.fields.map(renderField)}
                {activePanel.noteKey && (
                  <div
                    className='muted'
                    style={{
                      fontSize: 11,
                      marginTop: 8,
                      paddingTop: 12,
                      borderTop: '1px dashed var(--hf-rule)',
                    }}
                  >
                    {tr(
                      `console.admin.settings.${activePanel.noteKey}`,
                      activePanel.noteFallback,
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </HFShell>
  );
};

export default HFAdminSettings;
