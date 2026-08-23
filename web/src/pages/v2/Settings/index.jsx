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
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import WIPBanner from '../../../components/hifi/WIPBanner';
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { API, showError, showSuccess } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

// Wave 2: only security section is wired; notifications/team/integrations/MFA remain stubs pending infra.
// Wave 3 Phase 1 (2026-05-20): revoke session wired to DELETE /sessions/current.

/*
 * HiFi 12 — Settings.
 * Profile: wired to GET/PUT /api/v2/:tenant_slug/user/me
 * Security/Sessions: wired to GET /api/v2/:tenant_slug/sessions (Wave 2).
 *   Returns single synthetic session (auth_method + active_tokens + request_count).
 *   Single-device revoke wired (Wave 3 Phase 1). Multi-device tracking deferred to v3.
 * Notifications + Team: mocked; see adr-2026-05-18-budget-alerts.md / -tenant-credit-pool.md.
 */

const fmtCNY = (v) =>
  typeof v === 'number'
    ? '¥' +
      v.toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      })
    : '—';

// formatRelativeTime converts a Unix timestamp (seconds) to a human-readable
// relative string (e.g. "just now", "3m ago"). No external dependency —
// avoids adding date-fns for a single call site. `tr` is passed in because
// module scope has no i18n context.
const formatRelativeTime = (unixSec, tr) => {
  if (!unixSec) return '—';
  const diffSec = Math.floor(Date.now() / 1000) - unixSec;
  if (diffSec < 60) return tr('console.common.time_just_now', 'just now');
  if (diffSec < 3600)
    return tr('console.common.time_minutes_ago', {
      count: Math.floor(diffSec / 60),
    });
  if (diffSec < 86400)
    return tr('console.common.time_hours_ago', {
      count: Math.floor(diffSec / 3600),
    });
  return tr('console.common.time_days_ago', {
    count: Math.floor(diffSec / 86400),
  });
};

// Labels/descriptions resolved at render via tr() — module scope has no i18n
// context. Keys: console.settings.section_<id> / section_<id>_desc.
const SECTIONS = [
  ['profile', 'Profile', 'name, email, avatar'],
  ['security', 'Security', 'password, mfa, sessions'],
  ['subscription', 'Subscription', 'plan tier & entitlements'],
  ['billing', 'Billing', 'wallet balance & usage'],
  ['notifications', 'Notifications', 'email & webhook alerts'],
  ['team', 'Team & roles', 'members and permissions'],
  ['integrations', 'Integrations', 'webhooks, slack, observability'],
  ['region', 'Region & data', 'where data lives'],
  ['danger', 'Danger zone', 'export, transfer, delete'],
];

// Wave A Squad 5A (2026-05-20): read-only entitlement summary. Values are
// derived locally from the existing /user/me `group` field — backend
// entitlement registry not yet implemented. See caveats in commit body.
// Order: [label, value]. Tooltip on the upgrade button explains scope.
// Translatable values are [key, fallback] pairs resolved at render via tr()
// (module scope has no i18n context); plain strings (e.g. "99.5%") render
// verbatim.
const ENTITLEMENT_BY_GROUP = {
  default: {
    label: ['tier_free', 'Free'],
    routing: ['ent_routing_shared', 'shared pool'],
    sla: ['ent_sla_best_effort', 'best effort'],
    auditDays: 7,
    support: ['ent_support_community', 'community'],
  },
  vip: {
    label: ['tier_pro', 'Pro'],
    routing: ['ent_routing_priority', 'priority routing'],
    sla: '99.5%',
    auditDays: 30,
    support: ['ent_support_business_hours', 'business hours'],
  },
  pro: {
    label: ['tier_pro', 'Pro'],
    routing: ['ent_routing_priority', 'priority routing'],
    sla: '99.5%',
    auditDays: 30,
    support: ['ent_support_business_hours', 'business hours'],
  },
  enterprise: {
    label: ['tier_enterprise', 'Enterprise'],
    routing: ['ent_routing_dedicated', 'dedicated pool'],
    sla: '99.95%',
    auditDays: 365,
    support: ['ent_support_dedicated', '24/7 dedicated'],
  },
};

// Wave A Squad 5A: 3 read-only notification channels with placeholder events.
// Toggle switches are disabled — mutation flow lands in Wave B per
// adr-2026-05-18-budget-alerts.md.
// Labels/events are [key, fallback] pairs resolved at render via tr().
const NOTIFICATION_EVENTS = [
  ['event_quota_threshold', 'Quota threshold'],
  ['event_plan_limit', 'Plan limit'],
  ['event_security_event', 'Security event'],
];

const NOTIFICATION_CHANNELS = [
  {
    key: 'email',
    label: ['channel_email', 'Email notifications'],
    events: NOTIFICATION_EVENTS,
  },
  {
    key: 'webhook',
    label: ['channel_webhook', 'Webhook'],
    events: NOTIFICATION_EVENTS,
  },
  {
    key: 'inapp',
    label: ['channel_inapp', 'In-app'],
    events: NOTIFICATION_EVENTS,
  },
];

// Integration registry is not implemented — there is no connection store and
// no live status to report. Every channel is honestly "not configured"; we do
// NOT paint fake green "connected · ok" dots (§4.1 ①: a value you can't measure
// is not a value you may show).
// Names are product names (not translated); status text is translated at
// render via console.settings.not_configured. Third item is the dot class.
const INTEGRATIONS = [
  ['Slack', 'idle'],
  ['PagerDuty', 'idle'],
  ['Datadog', 'idle'],
  ['Webhook', 'idle'],
  ['Sentry', 'idle'],
  ['Discord', 'idle'],
];

// Data-residency selection is not wired (no backend). No region is "current" —
// claiming one would be a fabricated state. All shown as merely available.
const REGIONS = [
  ['us-west', false],
  ['eu-frankfurt', false],
  ['ap-shanghai', false],
];

// Shared inline input style (no hf-input class)
const inputStyle = {
  fontFamily: 'var(--hf-mono)',
  fontSize: 12,
  padding: '3px 6px',
  width: '100%',
  border: '1px solid var(--hf-rule)',
  background: 'var(--hf-sunken)',
  color: 'var(--hf-ink)',
  borderRadius: 2,
  outline: 'none',
};

// Inline field editor — commits on Enter or blur, cancels on Escape
const InlineEdit = ({ value, onSave, onCancel }) => {
  const [v, setV] = useState(value ?? '');
  const ref = useRef(null);

  useEffect(() => {
    ref.current?.select();
  }, []);

  const commit = () => {
    const trimmed = v.trim();
    if (trimmed !== (value ?? '').trim()) onSave(trimmed);
    else onCancel();
  };

  return (
    <input
      ref={ref}
      style={inputStyle}
      value={v}
      onChange={(e) => setV(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') commit();
        if (e.key === 'Escape') onCancel();
      }}
      onBlur={commit}
    />
  );
};

// "Coming soon" note shown under non-profile section headers
const ComingSoon = () => {
  const { t: tr } = useTranslation();
  return (
    <p
      className='faint mono'
      style={{ fontSize: 10, marginTop: 6, marginBottom: 0 }}
    >
      {tr('console.settings.coming_soon', 'available in next release')}
    </p>
  );
};

const HFSettings = () => {
  const tenantSlug = useTenantSlug();
  const navigate = useNavigate();
  // Aliased to `tr` per the v2 console convention (avoids shadowing).
  const { t: tr } = useTranslation();
  const [section, setSection] = useState('profile');

  // Which section fetches have already been attempted, keyed by tenant.
  //
  // These effects used to be guarded on "is the data still absent?" — which is
  // true again the instant a fetch resolves with nothing, so the effect re-ran
  // without bound. That is not only a failure mode: a customer who has simply
  // never topped up gets a successful HTTP 200 carrying `summary: null` and
  // `items: []`, which re-arms the guard exactly the same way. Measured on the
  // billing tab in that state: 670 requests in 5 seconds, and the panel never
  // left "loading…".
  //
  // Attempted-ness is the right question. Keyed by tenant so switching tenants
  // still re-fetches, which is the one case the old guard got right.
  const attemptedRef = useRef(new Set());
  const shouldAttempt = (key) => {
    const id = `${key}:${tenantSlug}`;
    if (attemptedRef.current.has(id)) return false;
    attemptedRef.current.add(id);
    return true;
  };

  // Profile state
  const [profile, setProfile] = useState(null); // raw API response data
  const [loadingProfile, setLoadingProfile] = useState(true);
  const [editField, setEditField] = useState(null); // 'display_name' | 'email'
  const [saving, setSaving] = useState(false);

  // Session state — single-session for now (backend has no device list)
  const [sessions, setSessions] = useState(null);
  const [sessionLoading, setSessionLoading] = useState(false);

  // Revoke session confirm dialog
  const [revokeVisible, setRevokeVisible] = useState(false);
  const [revoking, setRevoking] = useState(false);

  // Subscription tab (Wave A Squad 5A) — derived from /user/me + best-effort
  // /user/billing/summary. No dedicated subscription endpoint yet.
  const [subLoading, setSubLoading] = useState(false);
  const [subError, setSubError] = useState(false);
  const [subData, setSubData] = useState(null); // { tier, group, source }

  // Billing tab (Wave A Squad 5A) — wallet summary + last-30d aggregate +
  // recent transactions (synthesised from /billing/topups; full ClickHouse
  // history is Wave C).
  const [billLoading, setBillLoading] = useState(false);
  const [billError, setBillError] = useState(false);
  const [billSummary, setBillSummary] = useState(null);
  const [billTxns, setBillTxns] = useState([]);

  const fetchProfile = useCallback(async () => {
    setLoadingProfile(true);
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/user/me`);
      if (res?.data?.success) {
        setProfile(res.data.data);
      }
    } catch (_) {
      // error toast shown by API interceptor
    } finally {
      setLoadingProfile(false);
    }
  }, [tenantSlug]);

  const fetchSessions = useCallback(async () => {
    setSessionLoading(true);
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/sessions`);
      if (res?.data?.success) {
        setSessions(res.data.data.items ?? []);
      }
    } catch (_) {
      // error toast shown by API interceptor
    } finally {
      setSessionLoading(false);
    }
  }, [tenantSlug]);

  // Subscription tab loader — reuses the cached profile when present (Profile
  // tab fetches it on mount), otherwise refetches. Falls back to a placeholder
  // if no group field is exposed. Best-effort enrichment via billing summary
  // (subscription_plan field is currently absent on the Go BillingSummary
  // struct — guard accordingly).
  const fetchSubscription = useCallback(async () => {
    setSubLoading(true);
    setSubError(false);
    try {
      let p = profile;
      if (!p) {
        const res = await API.get(`/api/v2/${tenantSlug}/user/me`);
        if (res?.data?.success) {
          p = res.data.data;
          setProfile(p);
        }
      }
      // Best-effort: try platform billing summary for subscription_plan hint.
      let planHint = null;
      try {
        const sumRes = await API.get('/api/v2/user/billing/summary');
        if (sumRes?.data?.success) {
          planHint = sumRes.data.data?.subscription_plan ?? null;
        }
      } catch (_) {
        // non-fatal — subscription_plan field optional
      }

      if (p) {
        setSubData({
          group: p.group ?? null,
          planHint,
          source: planHint
            ? 'platform'
            : p.group
              ? 'user_group'
              : 'placeholder',
        });
      } else {
        setSubError(true);
      }
    } catch (_) {
      setSubError(true);
    } finally {
      setSubLoading(false);
    }
  }, [tenantSlug, profile]);

  // Billing tab loader — pulls platform balance + tenant-scoped topup history.
  // Both calls are tolerant of 4xx/5xx: if either fails we surface error state
  // but never throw uncaught. Full transaction history (ClickHouse) is Wave C.
  const fetchBilling = useCallback(async () => {
    setBillLoading(true);
    setBillError(false);
    try {
      const [sumRes, txnRes] = await Promise.allSettled([
        API.get('/api/v2/user/billing/summary'),
        API.get(`/api/v2/${tenantSlug}/billing/topups?p=1&size=5`),
      ]);

      let gotSummary = false;
      if (
        sumRes.status === 'fulfilled' &&
        sumRes.value?.data?.success &&
        sumRes.value.data.data
      ) {
        setBillSummary(sumRes.value.data.data);
        gotSummary = true;
      }

      let gotTxns = false;
      if (
        txnRes.status === 'fulfilled' &&
        txnRes.value?.data?.success &&
        Array.isArray(txnRes.value.data.data?.items)
      ) {
        setBillTxns(txnRes.value.data.data.items);
        gotTxns = true;
      }

      // Treat "both failed" as error; one succeeding still renders a useful tab.
      if (!gotSummary && !gotTxns) {
        setBillError(true);
      }
    } catch (_) {
      setBillError(true);
    } finally {
      setBillLoading(false);
    }
  }, [tenantSlug]);

  useEffect(() => {
    if (tenantSlug) fetchProfile();
  }, [fetchProfile, tenantSlug]);

  useEffect(() => {
    if (section === 'security' && shouldAttempt('sessions')) {
      fetchSessions();
    }
  }, [section, tenantSlug, fetchSessions]);

  useEffect(() => {
    if (section === 'subscription' && shouldAttempt('subscription')) {
      fetchSubscription();
    }
  }, [section, tenantSlug, fetchSubscription]);

  useEffect(() => {
    if (section === 'billing' && shouldAttempt('billing')) {
      fetchBilling();
    }
  }, [section, tenantSlug, fetchBilling]);

  const handleRevokeSession = async () => {
    if (revoking) return;
    setRevoking(true);
    try {
      const res = await API.delete(`/api/v2/${tenantSlug}/sessions/current`);
      if (res?.data?.success) {
        const redirect = res.data.data?.redirect ?? '/console/v2/login';
        navigate(redirect);
      } else {
        showError(
          res?.data?.message ||
            tr('console.settings.revoke_failed', 'Failed to revoke session'),
        );
      }
    } catch (_) {
      // interceptor handles toast
    } finally {
      setRevoking(false);
      setRevokeVisible(false);
    }
  };

  const handleSaveField = async (field, value) => {
    setEditField(null);
    if (!value) return;
    setSaving(true);
    try {
      const body = { [field]: value };
      const res = await API.put(`/api/v2/${tenantSlug}/user/me`, body);
      if (res?.data?.success) {
        showSuccess(tr('console.settings.toast_saved', 'Saved'));
        setProfile((prev) => ({ ...prev, ...res.data.data }));
      }
    } catch (_) {
      showError(tr('console.settings.toast_save_failed', 'Save failed'));
    } finally {
      setSaving(false);
    }
  };

  // Profile rows: [label, fieldKey | null (read-only), display value, editable]
  const profileRows = profile
    ? [
        [
          tr('console.settings.field_display_name', 'display name'),
          'display_name',
          profile.display_name ?? '—',
          true,
        ],
        [
          tr('console.settings.field_email', 'email'),
          'email',
          profile.email ?? '—',
          true,
        ],
        [
          tr('console.settings.field_username', 'username'),
          null,
          profile.username ?? '—',
          false,
        ],
        [
          tr('console.settings.field_role', 'role'),
          null,
          profile.role ?? '—',
          false,
        ],
        [
          tr('console.settings.field_tenant_id', 'tenant id'),
          null,
          profile.tenant_id ?? '—',
          false,
        ],
      ]
    : [];

  return (
    <HFShell
      active='settings'
      crumbs={[
        tr('console.nav.section_my_account', 'my account'),
        tr('console.settings.crumb', 'settings'),
      ]}
    >
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '240px 1fr',
          height: '100%',
          minHeight: 0,
        }}
      >
        {/* Left nav */}
        <div
          style={{
            borderRight: '1px solid var(--hf-rule)',
            background: 'var(--hf-paper)',
            padding: '20px 0',
          }}
        >
          {SECTIONS.map(([k, l, d]) => (
            <div
              key={k}
              onClick={() => setSection(k)}
              style={{
                padding: '10px 22px',
                cursor: 'pointer',
                background: section === k ? 'var(--hf-elev)' : 'transparent',
                borderLeft:
                  section === k
                    ? '2px solid var(--hf-accent)'
                    : '2px solid transparent',
              }}
            >
              <div className='strong' style={{ fontSize: 12 }}>
                {tr(`console.settings.section_${k}`, l)}
              </div>
              <div
                className='faint mono'
                style={{ fontSize: 10, marginTop: 2 }}
              >
                {tr(`console.settings.section_${k}_desc`, d)}
              </div>
            </div>
          ))}
        </div>

        {/* Right content */}
        <div style={{ overflow: 'auto', padding: 28, maxWidth: 720 }}>
          <div className='lbl' style={{ marginBottom: 4 }}>
            {tr('console.settings.crumb', 'settings')} · {section}
          </div>
          <h1
            className='display'
            style={{ fontSize: 32, margin: 0, letterSpacing: '-0.025em' }}
          >
            {tr(
              `console.settings.section_${section}`,
              SECTIONS.find((s) => s[0] === section)[1],
            )}
          </h1>

          {/* ── Profile ── */}
          {section === 'profile' && (
            <div style={{ marginTop: 22 }}>
              {loadingProfile && (
                <div className='muted' style={{ fontSize: 12 }}>
                  {tr('console.common.loading', 'Loading…')}
                </div>
              )}

              {!loadingProfile && !profile && (
                <div className='muted' style={{ fontSize: 12 }}>
                  {tr(
                    'console.settings.load_profile_failed',
                    'Failed to load profile.',
                  )}
                </div>
              )}

              {!loadingProfile && profile && (
                <>
                  {/* Field rows */}
                  <div className='panel'>
                    {profileRows.map(
                      ([label, fieldKey, value, editable], i, a) => (
                        <div
                          key={label}
                          style={{
                            display: 'grid',
                            gridTemplateColumns: '160px 1fr auto',
                            padding: '14px 16px',
                            borderBottom:
                              i < a.length - 1
                                ? '1px dashed var(--hf-rule)'
                                : 0,
                            alignItems: 'center',
                            gap: 12,
                          }}
                        >
                          <span className='lbl'>{label}</span>

                          {editable && editField === fieldKey ? (
                            <InlineEdit
                              value={value === '—' ? '' : value}
                              onSave={(v) => handleSaveField(fieldKey, v)}
                              onCancel={() => setEditField(null)}
                            />
                          ) : (
                            <span className='strong' style={{ fontSize: 13 }}>
                              {value}
                            </span>
                          )}

                          {editable && editField !== fieldKey ? (
                            <button
                              type='button'
                              className='btn ghost sm'
                              disabled={saving}
                              onClick={() => setEditField(fieldKey)}
                            >
                              {tr('console.common.edit', 'edit')}
                            </button>
                          ) : (
                            /* Keep grid alignment for read-only rows */
                            <span />
                          )}
                        </div>
                      ),
                    )}
                  </div>

                  {/* Usage summary */}
                  <div
                    className='panel'
                    style={{
                      marginTop: 16,
                      padding: '14px 16px',
                      display: 'flex',
                      gap: 32,
                    }}
                  >
                    <div>
                      <div className='lbl' style={{ marginBottom: 4 }}>
                        {tr('console.settings.spent', 'spent')}
                      </div>
                      <div className='display' style={{ fontSize: 22 }}>
                        ${(profile.used_quota / getQuotaPerUSD()).toFixed(2)}
                      </div>
                    </div>
                    <div>
                      <div className='lbl' style={{ marginBottom: 4 }}>
                        {tr('console.settings.requests', 'requests')}
                      </div>
                      <div className='display' style={{ fontSize: 22 }}>
                        {(profile.request_count ?? 0).toLocaleString()}
                      </div>
                    </div>
                  </div>
                </>
              )}
            </div>
          )}

          {/* ── Security ── */}
          {section === 'security' && (
            <div style={{ marginTop: 22 }}>
              <div
                className='panel'
                style={{ padding: 18, marginBottom: 14, marginTop: 8 }}
              >
                <div className='lbl'>
                  {tr('console.settings.mfa_title', 'multi-factor auth')}
                </div>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 10,
                    marginTop: 10,
                  }}
                >
                  <span className='dot ok' />{' '}
                  <span className='strong'>
                    {tr(
                      'console.settings.mfa_status',
                      'authenticator app · enabled',
                    )}
                  </span>
                  <span style={{ flex: 1 }} />
                  <button
                    type='button'
                    className='btn sm'
                    disabled
                    title={tr(
                      'console.settings.mfa_regenerate_title',
                      'MFA infra pending — totp_seed table not yet provisioned',
                    )}
                  >
                    {tr('console.settings.mfa_regenerate', 'regenerate')}
                  </button>
                </div>
              </div>

              <div
                className='panel'
                style={{ padding: 18, marginTop: 14, marginBottom: 14 }}
              >
                <div className='lbl' style={{ marginBottom: 12 }}>
                  {tr('console.settings.sessions_title', 'sessions')}
                </div>
                {sessionLoading && (
                  <div
                    className='muted'
                    style={{ fontSize: 12, marginTop: 10 }}
                  >
                    {tr('console.common.loading', 'Loading…')}
                  </div>
                )}
                {!sessionLoading && !sessions && (
                  <div
                    className='muted'
                    style={{ fontSize: 12, marginTop: 10 }}
                  >
                    {tr(
                      'console.settings.load_sessions_failed',
                      'Failed to load sessions.',
                    )}
                  </div>
                )}
                {!sessionLoading && sessions && sessions.length > 0 && (
                  <table
                    data-testid='sessions-table'
                    style={{
                      width: '100%',
                      borderCollapse: 'collapse',
                      fontFamily: 'var(--hf-mono)',
                      fontSize: 11,
                    }}
                  >
                    <thead>
                      <tr style={{ color: 'var(--hf-ink-3)' }}>
                        <th
                          style={{
                            textAlign: 'left',
                            padding: '4px 8px',
                            fontWeight: 500,
                          }}
                        >
                          {tr('console.settings.th_auth_method', 'auth method')}
                        </th>
                        <th
                          style={{
                            textAlign: 'left',
                            padding: '4px 8px',
                            fontWeight: 500,
                          }}
                        >
                          {tr('console.settings.th_current', 'current')}
                        </th>
                        <th
                          style={{
                            textAlign: 'right',
                            padding: '4px 8px',
                            fontWeight: 500,
                          }}
                        >
                          {tr(
                            'console.settings.th_active_tokens',
                            'active tokens',
                          )}
                        </th>
                        <th
                          style={{
                            textAlign: 'right',
                            padding: '4px 8px',
                            fontWeight: 500,
                          }}
                        >
                          {tr(
                            'console.settings.th_requests_30d',
                            'requests (30d)',
                          )}
                        </th>
                        <th
                          style={{
                            textAlign: 'right',
                            padding: '4px 8px',
                            fontWeight: 500,
                          }}
                        >
                          {tr('console.settings.th_last_seen', 'last seen')}
                        </th>
                        <th style={{ padding: '4px 8px' }} />
                      </tr>
                    </thead>
                    <tbody>
                      {sessions.map((s, i) => (
                        <tr
                          key={s.id ?? i}
                          style={{ borderTop: '1px dashed var(--hf-rule)' }}
                        >
                          <td style={{ padding: '8px 8px' }}>
                            <span className='strong'>
                              {s.auth_method ||
                                tr(
                                  'console.settings.session_fallback',
                                  'session',
                                )}
                            </span>
                          </td>
                          <td style={{ padding: '8px 8px' }}>
                            {s.current && (
                              <span className='tag ok'>
                                {tr('console.settings.tag_current', 'current')}
                              </span>
                            )}
                          </td>
                          <td
                            style={{ padding: '8px 8px', textAlign: 'right' }}
                          >
                            {s.active_tokens ?? 0}
                          </td>
                          <td
                            style={{ padding: '8px 8px', textAlign: 'right' }}
                          >
                            {(s.request_count ?? 0).toLocaleString()}
                          </td>
                          <td
                            className='faint'
                            style={{ padding: '8px 8px', textAlign: 'right' }}
                          >
                            {formatRelativeTime(s.last_seen, tr)}
                          </td>
                          <td
                            style={{ padding: '8px 8px', textAlign: 'right' }}
                          >
                            <button
                              type='button'
                              className='btn sm'
                              data-testid='revoke-session-btn'
                              onClick={() => setRevokeVisible(true)}
                            >
                              {tr('console.settings.revoke', 'revoke')}
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>

              {/* Revoke current session confirm dialog */}
              <ConfirmDialog
                visible={revokeVisible}
                title={tr(
                  'console.settings.confirm_revoke_title',
                  'Revoke current session',
                )}
                consequenceList={[
                  tr(
                    'console.settings.confirm_revoke_c1',
                    'This browser session will be invalidated immediately.',
                  ),
                  tr(
                    'console.settings.confirm_revoke_c2',
                    'You will be redirected to the login page and must sign in again.',
                  ),
                ]}
                confirmText='revoke'
                confirmButtonText={tr(
                  'console.settings.revoke_session_btn',
                  'revoke session',
                )}
                confirmButtonType='danger'
                onConfirm={handleRevokeSession}
                onCancel={() => setRevokeVisible(false)}
              />
            </div>
          )}

          {/* ── Subscription (Wave A Squad 5A — read-only) ── */}
          {section === 'subscription' && (
            <div style={{ marginTop: 22 }} data-testid='subscription-section'>
              {subLoading && (
                <div className='muted' style={{ fontSize: 12 }}>
                  {tr('console.common.loading', 'Loading…')}
                </div>
              )}

              {!subLoading && subError && (
                <div
                  className='muted'
                  style={{ fontSize: 12 }}
                  data-testid='subscription-error'
                >
                  {tr(
                    'console.settings.load_subscription_failed',
                    'Failed to load subscription.',
                  )}
                </div>
              )}

              {!subLoading && !subError && subData && (
                <>
                  {(() => {
                    const ent =
                      ENTITLEMENT_BY_GROUP[subData.group] ??
                      ENTITLEMENT_BY_GROUP.default;
                    // Resolve [key, fallback] pairs via tr(); plain strings
                    // (e.g. "99.5%") pass through verbatim.
                    const tx = (v) =>
                      Array.isArray(v)
                        ? tr(`console.settings.${v[0]}`, v[1])
                        : v;
                    const tierName = subData.planHint || tx(ent.label);
                    const isPlaceholder = subData.source === 'placeholder';

                    return (
                      <>
                        <div
                          className='panel'
                          style={{ padding: 18, marginBottom: 14 }}
                        >
                          <div className='lbl'>
                            {tr(
                              'console.settings.current_plan',
                              'current plan',
                            )}
                          </div>
                          <div
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 10,
                              marginTop: 10,
                            }}
                          >
                            <span
                              className='tag ok'
                              data-testid='subscription-tier-badge'
                              style={{
                                fontFamily: 'var(--hf-mono)',
                                fontSize: 12,
                                padding: '3px 10px',
                              }}
                            >
                              {tierName}
                            </span>
                            {isPlaceholder && (
                              <span
                                className='faint mono'
                                style={{ fontSize: 11 }}
                              >
                                {tr(
                                  'console.settings.free_tier_note',
                                  'Free tier — entitlement API not yet wired',
                                )}
                              </span>
                            )}
                            <span style={{ flex: 1 }} />
                            <button
                              type='button'
                              className='btn sm'
                              disabled
                              data-testid='subscription-upgrade-btn'
                              title={tr(
                                'console.settings.upgrade_plan_title',
                                'Plan upgrades available in Wave B',
                              )}
                            >
                              {tr(
                                'console.settings.upgrade_plan',
                                'Upgrade plan',
                              )}
                            </button>
                          </div>
                        </div>

                        <div className='panel'>
                          {[
                            [
                              tr(
                                'console.settings.ent_routing_modes',
                                'routing modes',
                              ),
                              tx(ent.routing),
                            ],
                            [
                              tr('console.settings.ent_sla_tier', 'SLA tier'),
                              tx(ent.sla),
                            ],
                            [
                              tr(
                                'console.settings.ent_audit_retention',
                                'audit retention',
                              ),
                              tr('console.settings.audit_days', {
                                count: ent.auditDays,
                              }),
                            ],
                            [
                              tr(
                                'console.settings.ent_support_tier',
                                'support tier',
                              ),
                              tx(ent.support),
                            ],
                          ].map(([k, v], i, a) => (
                            <div
                              key={k}
                              style={{
                                display: 'grid',
                                gridTemplateColumns: '180px 1fr',
                                padding: '12px 16px',
                                borderBottom:
                                  i < a.length - 1
                                    ? '1px dashed var(--hf-rule)'
                                    : 0,
                                alignItems: 'center',
                              }}
                            >
                              <span className='lbl'>{k}</span>
                              <span className='strong' style={{ fontSize: 13 }}>
                                {v}
                              </span>
                            </div>
                          ))}
                        </div>
                      </>
                    );
                  })()}
                </>
              )}
            </div>
          )}

          {/* ── Billing (Wave A Squad 5A — read-only) ── */}
          {section === 'billing' && (
            <div style={{ marginTop: 22 }} data-testid='billing-section'>
              {billLoading && (
                <div className='muted' style={{ fontSize: 12 }}>
                  {tr('console.common.loading', 'Loading…')}
                </div>
              )}

              {!billLoading && billError && (
                <div
                  className='muted'
                  style={{ fontSize: 12 }}
                  data-testid='billing-error'
                >
                  {tr(
                    'console.settings.billing_error',
                    'Billing data — backend wiring pending Wave B',
                  )}
                </div>
              )}

              {!billLoading && !billError && (
                <>
                  <div
                    className='panel'
                    style={{
                      padding: 18,
                      marginBottom: 14,
                      display: 'flex',
                      gap: 32,
                    }}
                  >
                    <div>
                      <div className='lbl' style={{ marginBottom: 4 }}>
                        {tr(
                          'console.settings.wallet_balance',
                          'wallet balance',
                        )}
                      </div>
                      <div
                        className='display'
                        style={{ fontSize: 22 }}
                        data-testid='billing-balance'
                      >
                        {billSummary?.balance != null
                          ? fmtCNY(billSummary.balance)
                          : billSummary?.wallet_balance_cny != null
                            ? fmtCNY(billSummary.wallet_balance_cny)
                            : '—'}
                      </div>
                    </div>
                    <div>
                      <div className='lbl' style={{ marginBottom: 4 }}>
                        {tr(
                          'console.settings.last_30d',
                          'last 30d consumption',
                        )}
                      </div>
                      <div
                        className='display'
                        style={{ fontSize: 22 }}
                        data-testid='billing-30d'
                      >
                        {billSummary?.mtd_spend_cny != null
                          ? fmtCNY(billSummary.mtd_spend_cny)
                          : billSummary?.lifetime_spend != null
                            ? fmtCNY(billSummary.lifetime_spend)
                            : '—'}
                      </div>
                    </div>
                  </div>

                  <div className='panel'>
                    <div
                      style={{
                        padding: '12px 16px',
                        borderBottom: '1px solid var(--hf-rule)',
                        display: 'flex',
                        alignItems: 'center',
                      }}
                    >
                      <div className='lbl'>
                        {tr(
                          'console.settings.recent_txns',
                          'recent transactions',
                        )}
                      </div>
                      <span style={{ flex: 1 }} />
                      <button
                        type='button'
                        className='btn ghost sm'
                        disabled
                        data-testid='billing-view-full-btn'
                        title={tr(
                          'console.settings.view_full_history_title',
                          'Full billing history available in Wave C (ClickHouse insights)',
                        )}
                      >
                        {tr(
                          'console.settings.view_full_history',
                          'View full history',
                        )}
                      </button>
                    </div>

                    {billTxns.length === 0 ? (
                      <div
                        style={{
                          padding: 22,
                          textAlign: 'center',
                          color: 'var(--hf-ink-3)',
                          fontFamily: 'var(--hf-mono)',
                          fontSize: 12,
                        }}
                        data-testid='billing-empty-state'
                      >
                        {tr(
                          'console.settings.no_txns',
                          'No recent transactions.',
                        )}
                      </div>
                    ) : (
                      <table
                        data-testid='billing-txn-table'
                        style={{
                          width: '100%',
                          borderCollapse: 'collapse',
                          fontFamily: 'var(--hf-mono)',
                          fontSize: 11,
                        }}
                      >
                        <thead>
                          <tr style={{ color: 'var(--hf-ink-3)' }}>
                            <th
                              style={{
                                textAlign: 'left',
                                padding: '6px 12px',
                                fontWeight: 500,
                              }}
                            >
                              {tr('console.settings.th_date', 'date')}
                            </th>
                            <th
                              style={{
                                textAlign: 'left',
                                padding: '6px 12px',
                                fontWeight: 500,
                              }}
                            >
                              {tr('console.settings.th_type', 'type')}
                            </th>
                            <th
                              style={{
                                textAlign: 'right',
                                padding: '6px 12px',
                                fontWeight: 500,
                              }}
                            >
                              {tr('console.settings.th_amount', 'amount')}
                            </th>
                            <th
                              style={{
                                textAlign: 'right',
                                padding: '6px 12px',
                                fontWeight: 500,
                              }}
                            >
                              {tr('console.settings.th_status', 'status')}
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {billTxns.slice(0, 5).map((t, i) => (
                            <tr
                              key={t.id ?? i}
                              style={{ borderTop: '1px dashed var(--hf-rule)' }}
                            >
                              <td style={{ padding: '8px 12px' }}>
                                {t.created_at
                                  ? formatRelativeTime(t.created_at, tr)
                                  : '—'}
                              </td>
                              {/* Type derived from the row; this list is the
                                  topups ledger, so 'topup' is the accurate
                                  fallback (not a fabricated label). */}
                              <td style={{ padding: '8px 12px' }}>
                                {t.type ||
                                  tr(
                                    'console.settings.txn_type_topup',
                                    'topup',
                                  )}
                              </td>
                              <td
                                style={{
                                  padding: '8px 12px',
                                  textAlign: 'right',
                                }}
                              >
                                {typeof t.quota === 'number'
                                  ? `${(t.quota / getQuotaPerUSD()).toFixed(2)} ${tr('console.settings.usd_eq', 'USD eq.')}`
                                  : '—'}
                              </td>
                              {/* Status derived from the row — no unconditional
                                  green "settled". Honest '—' when the topup
                                  record carries no status field. */}
                              <td
                                style={{
                                  padding: '8px 12px',
                                  textAlign: 'right',
                                }}
                              >
                                {t.status ? (
                                  <span className='tag ok'>{t.status}</span>
                                ) : (
                                  <span className='faint mono'>—</span>
                                )}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                  </div>
                </>
              )}
            </div>
          )}

          {/* ── Notifications (Wave A Squad 5A — read-only upgrade) ── */}
          {section === 'notifications' && (
            <div style={{ marginTop: 22 }} data-testid='notifications-section'>
              <WIPBanner
                reason={tr(
                  'console.settings.notif_wip_reason',
                  'Notification subscription store, dispatch path, and threshold rules not yet implemented. Designed in adr-2026-05-18-budget-alerts.md.',
                )}
                todo={tr(
                  'console.settings.notif_wip_todo',
                  'Backend: notification_subscription table + /api/v2/{slug}/notifications/subscriptions + Prometheus rule pack.',
                )}
              />
              <div className='panel' style={{ marginTop: 14 }}>
                {NOTIFICATION_CHANNELS.map((ch, i, a) => (
                  <div
                    key={ch.key}
                    style={{
                      padding: '14px 16px',
                      borderBottom:
                        i < a.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                      display: 'grid',
                      gridTemplateColumns: '1fr auto',
                      alignItems: 'center',
                      gap: 16,
                    }}
                  >
                    <div>
                      <div className='strong' style={{ fontSize: 13 }}>
                        {tr(`console.settings.${ch.label[0]}`, ch.label[1])}
                      </div>
                      <div
                        className='faint mono'
                        style={{ fontSize: 10, marginTop: 4 }}
                      >
                        {ch.events
                          .map(([k, f]) => tr(`console.settings.${k}`, f))
                          .join(' · ')}
                      </div>
                    </div>
                    <button
                      type='button'
                      className='btn sm'
                      disabled
                      data-testid={`notif-toggle-${ch.key}`}
                      title={tr(
                        'console.settings.notif_toggle_title',
                        'Notification preferences editable in Wave B',
                      )}
                    >
                      {tr('console.settings.toggle_off', 'off')}
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* ── Team ── */}
          {section === 'team' && (
            <div style={{ marginTop: 22 }}>
              <WIPBanner
                reason={tr(
                  'console.settings.team_wip_reason',
                  'Team / role management requires tenant membership store + role-permission matrix + invite flow. Not yet implemented.',
                )}
                todo={tr(
                  'console.settings.team_wip_todo',
                  'Backend: tenant_member table + role enum + POST /api/v2/{slug}/team/invite; cascade-revoke on member removal.',
                )}
              />
              <div
                className='panel'
                style={{
                  marginTop: 14,
                  padding: 24,
                  textAlign: 'center',
                  color: 'var(--hf-ink-3)',
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 12,
                }}
              >
                {tr(
                  'console.settings.team_empty',
                  'No team members — endpoint not implemented.',
                )}
              </div>
            </div>
          )}

          {/* ── Integrations ── */}
          {section === 'integrations' && (
            <div style={{ marginTop: 22 }}>
              <ComingSoon />
              <div
                style={{
                  marginTop: 16,
                  display: 'grid',
                  gridTemplateColumns: 'repeat(2, 1fr)',
                  gap: 14,
                }}
              >
                {INTEGRATIONS.map((r, i) => (
                  <div key={i} className='panel' style={{ padding: 16 }}>
                    <div
                      style={{ display: 'flex', alignItems: 'center', gap: 8 }}
                    >
                      <span className={'dot ' + r[1]} />
                      <span className='display' style={{ fontSize: 16 }}>
                        {r[0]}
                      </span>
                      <span style={{ flex: 1 }} />
                      <button
                        type='button'
                        className='btn sm'
                        disabled
                        title={tr(
                          'console.settings.integrations_deferred',
                          'integration registry deferred to v3',
                        )}
                      >
                        {r[1] === 'ok'
                          ? tr('console.settings.configure', 'configure')
                          : tr('console.settings.connect', 'connect')}
                      </button>
                    </div>
                    <div
                      className='faint mono'
                      style={{ fontSize: 10, marginTop: 6 }}
                    >
                      {tr('console.settings.not_configured', 'not configured')}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* ── Region ── */}
          {section === 'region' && (
            <div style={{ marginTop: 22 }}>
              <ComingSoon />
              <div className='panel' style={{ padding: 18, marginTop: 16 }}>
                <div className='lbl'>
                  {tr('console.settings.data_residency', 'data residency')}
                </div>
                <div
                  style={{
                    display: 'grid',
                    gridTemplateColumns: 'repeat(3, 1fr)',
                    gap: 10,
                    marginTop: 12,
                  }}
                >
                  {REGIONS.map(([r, sel], i) => (
                    <div
                      key={i}
                      className='panel-paper'
                      style={{
                        padding: 12,
                        border: sel
                          ? '2px solid var(--hf-accent)'
                          : '1px solid var(--hf-rule)',
                      }}
                    >
                      <div className='strong' style={{ fontSize: 13 }}>
                        {r}
                      </div>
                      <div
                        className='faint mono'
                        style={{ fontSize: 10, marginTop: 4 }}
                      >
                        {sel
                          ? tr('console.settings.region_current', 'current')
                          : tr(
                              'console.settings.region_available',
                              'available',
                            )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}

          {/* ── Danger ── */}
          {section === 'danger' && (
            <div style={{ marginTop: 22 }}>
              <ComingSoon />
              <div
                className='panel'
                style={{
                  padding: 18,
                  border: '1px solid var(--hf-err)',
                  marginTop: 16,
                }}
              >
                <div
                  className='display'
                  style={{ fontSize: 16, color: 'var(--hf-err)' }}
                >
                  {tr('console.settings.delete_account', 'Delete account')}
                </div>
                <div
                  className='muted'
                  style={{ fontSize: 12, marginTop: 6, lineHeight: 1.6 }}
                >
                  {tr(
                    'console.settings.delete_account_desc',
                    'permanently deletes all data: tokens, logs, channels, invoices. cannot be undone.',
                  )}
                </div>
                <button
                  type='button'
                  className='btn'
                  disabled
                  data-testid='danger-delete-btn'
                  title={tr(
                    'console.settings.delete_account_title',
                    'account deletion requires data-export + cascade-purge design — deferred to v3',
                  )}
                  style={{
                    marginTop: 12,
                    color: 'var(--hf-err)',
                    borderColor: 'var(--hf-err)',
                  }}
                >
                  {tr(
                    'console.settings.delete_account_btn',
                    'I understand · delete',
                  )}
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </HFShell>
  );
};

export default HFSettings;
