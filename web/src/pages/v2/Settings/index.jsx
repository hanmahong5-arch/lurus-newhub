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
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { API, showError, showSuccess } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { TotpService } from '../../../services/secureVerification';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

// Wave 2: security, subscription and billing sections are wired.
// Integrations/region/danger remain stubs pending infra (ComingSoon).
//
// Notifications (2026-09-16): real subscription, not a placeholder. The
// store (entity.User.Setting, JSON blob) and write path already existed —
// PUT /api/user/setting (user.go:521-535) — and dispatch already runs on
// the live relay consumption path via app.NotifyUser
// (internal/app/user_notify.go:53), called from checkAndSendQuotaNotify
// (internal/app/quota.go:1390). This panel is a second consumer of that
// same store alongside components/settings/PersonalSetting.jsx, not a new
// backend.
//
// Team (2026-09-16): retired as a newhub feature, not stubbed. Account and
// membership lifecycle belongs to the platform identity service — this
// service is a relying party, not the source of truth for who is on a
// tenant (see repo CLAUDE.md, "Auth"). identity.lurus.cn has no
// customer-facing team/member screen to link to (its authenticated nav is
// wallet/topup/subscriptions/invoices/refunds/redeem/account/data-privacy;
// the only org-membership screen is an internal /admin/v1 tool), so this
// section states plainly that the capability is not offered rather than
// linking somewhere that cannot serve it.
// Wave 3 Phase 1 (2026-05-20): revoke session wired to DELETE /sessions/current.

/*
 * HiFi 12 — Settings.
 * Profile: wired to GET/PUT /api/v2/:tenant_slug/user/me
 * Security/Sessions: wired to GET /api/v2/:tenant_slug/sessions (Wave 2).
 *   Returns single synthetic session (auth_method + active_tokens + request_count).
 *   Single-device revoke wired (Wave 3 Phase 1). Multi-device tracking deferred to v3.
 * Notifications: wired to GET setting-string on /api/v2/:tenant_slug/user/me
 *   and PUT /api/user/setting (Wave 2 field names, unchanged).
 * Team: not offered — no outbound link, since identity.lurus.cn has no
 *   customer-facing surface for it either.
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
  ['subscription', 'Subscription', 'routing group & entitlements'],
  ['billing', 'Billing', 'wallet balance & usage'],
  [
    'notifications',
    'Notifications',
    'quota alerts: email, webhook, bark, gotify',
  ],
  ['team', 'Team & roles', 'not available in this product yet'],
  ['integrations', 'Integrations', 'webhooks, slack, observability'],
  ['region', 'Region & data', 'where data lives'],
  ['danger', 'Danger zone', 'export, transfer, delete'],
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

  // Session state. With SESSION_REGISTRY_ENABLED off (or on but this login
  // registered no row — e.g. a bearer/token-authenticated visit), `sessions`
  // stays the single synthetic row it always was; with the flag on it can
  // hold one row per registered device (L7).
  const [sessions, setSessions] = useState(null);
  const [sessionLoading, setSessionLoading] = useState(false);

  // Revoke session confirm dialog — used for BOTH "revoke my current
  // session" (revokeTargetId === null, hits /sessions/current) and "revoke
  // this other device" (revokeTargetId = that row's numeric id, hits
  // /sessions/:id). Kept as one dialog since the two only differ in which
  // endpoint handleRevokeSession calls.
  const [revokeVisible, setRevokeVisible] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [revokeTargetId, setRevokeTargetId] = useState(null);

  // "Sign out other devices" confirm dialog (L7) — only ever shown when the
  // registry returned more than one row, so there is something to sign out.
  const [revokeOthersVisible, setRevokeOthersVisible] = useState(false);
  const [revokingOthers, setRevokingOthers] = useState(false);

  // Real TOTP state. This panel used to render a green dot and the words
  // "authenticator app · enabled" unconditionally — for every user, whether or
  // not they had ever enrolled — and disabled its button with the reason "MFA
  // infra pending — totp_seed table not yet provisioned". That reason was
  // false: the backend has had /api/user/totp/{status,enroll,confirm,disable}
  // (router/api-router.go) and the full enrolment UI has been live at
  // /console/personal all along. So the one security control a user might check
  // before trusting us told them they were protected when they were not.
  //
  // null = not loaded yet, false = the status call failed. Both render as
  // "unknown", never as a green light: claiming MFA is on is the failure mode
  // that matters here.
  const [totpStatus, setTotpStatus] = useState(null);
  const [totpLoading, setTotpLoading] = useState(false);

  const fetchTotpStatus = useCallback(async () => {
    setTotpLoading(true);
    try {
      setTotpStatus(await TotpService.getStatus());
    } catch (_) {
      setTotpStatus(false);
    } finally {
      setTotpLoading(false);
    }
  }, []);

  // Subscription tab (Wave A Squad 5A) — derived from /user/me only. No
  // dedicated subscription endpoint exists, so this tab does not call
  // /user/billing/summary (the Billing tab below owns that call).
  const [subLoading, setSubLoading] = useState(false);
  const [subError, setSubError] = useState(false);
  const [subData, setSubData] = useState(null); // { group }

  // Billing tab (Wave A Squad 5A) — wallet summary + last-30d aggregate +
  // recent transactions (synthesised from /billing/topups; full ClickHouse
  // history is Wave C).
  const [billLoading, setBillLoading] = useState(false);
  const [billError, setBillError] = useState(false);
  const [billSummary, setBillSummary] = useState(null);
  const [billTxns, setBillTxns] = useState([]);

  // Notifications tab (2026-09-16) — real subscription against the
  // store PersonalSetting.jsx already writes: entity.User.Setting (a JSON
  // blob), read back via the `setting` string on GET /api/v2/:slug/user/me
  // and written via PUT /api/user/setting (user.go:521-535 field names,
  // copied verbatim below — do not invent new ones). Seeded once from
  // profile.setting so a save from this panel never clobbers
  // accept_unset_model_ratio_model / record_ip_log — fields this panel has
  // no editor for — back to their zero values.
  const [notifySeeded, setNotifySeeded] = useState(false);
  const [notifySaving, setNotifySaving] = useState(false);
  const [notifyForm, setNotifyForm] = useState({
    notifyType: 'email',
    quotaWarningThreshold: 100000,
    webhookUrl: '',
    webhookSecret: '',
    notificationEmail: '',
    barkUrl: '',
    gotifyUrl: '',
    gotifyToken: '',
    gotifyPriority: 5,
    acceptUnsetModelRatioModel: false,
    recordIpLog: false,
  });

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
  // tab fetches it on mount), otherwise refetches. There is no backend
  // entitlement registry: the tab reads exactly the `group` field /user/me
  // already exposes and does not enrich it from anywhere else — a plan name
  // invented from that string would be a claim this deployment cannot back.
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

      if (p) {
        setSubData({ group: p.group ?? null });
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
    // TOTP is a per-user credential, not tenant-scoped, so this does not gate
    // on tenantSlug the way the panels above do.
    if (section === 'security' && totpStatus === null && !totpLoading) {
      fetchTotpStatus();
    }
  }, [section, totpStatus, totpLoading, fetchTotpStatus]);

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

  // Seed the notifications form from profile.setting exactly once profile
  // has loaded. Guarded on notifySeeded (not on `profile` truthiness alone)
  // so a later profile refetch — e.g. after editing display_name — does not
  // stomp on in-progress edits in this form.
  useEffect(() => {
    if (!profile || notifySeeded) return;
    let parsed = {};
    try {
      parsed = profile.setting ? JSON.parse(profile.setting) : {};
    } catch (_) {
      parsed = {};
    }
    setNotifyForm({
      notifyType: parsed.notify_type || 'email',
      quotaWarningThreshold: parsed.quota_warning_threshold || 100000,
      webhookUrl: parsed.webhook_url || '',
      webhookSecret: parsed.webhook_secret || '',
      notificationEmail: parsed.notification_email || '',
      barkUrl: parsed.bark_url || '',
      gotifyUrl: parsed.gotify_url || '',
      gotifyToken: parsed.gotify_token || '',
      gotifyPriority:
        parsed.gotify_priority !== undefined ? parsed.gotify_priority : 5,
      acceptUnsetModelRatioModel:
        parsed.accept_unset_model_ratio_model || false,
      recordIpLog: parsed.record_ip_log || false,
    });
    setNotifySeeded(true);
  }, [profile, notifySeeded]);

  // handleRevokeSession serves both dialogs the confirm at the bottom of
  // this component wires to: revokeTargetId === null means "revoke MY OWN
  // current session" (the original Wave 3 behaviour — logs the caller out
  // and redirects to /login); a numeric revokeTargetId means "revoke that
  // OTHER device by id" (L7) — the caller stays logged in, so this just
  // refetches the list instead of navigating away.
  const handleRevokeSession = async () => {
    if (revoking) return;
    setRevoking(true);
    try {
      if (revokeTargetId == null) {
        const res = await API.delete(`/api/v2/${tenantSlug}/sessions/current`);
        if (res?.data?.success) {
          // Fallback must be a route that exists: the SPA's only login route
          // is /login ('/console/v2/login' is not in the v2 route table).
          const redirect = res.data.data?.redirect ?? '/login';
          navigate(redirect);
        } else {
          showError(
            res?.data?.message ||
              tr('console.settings.revoke_failed', 'Failed to revoke session'),
          );
        }
      } else {
        // 204 No Content on success — there is no res.data to check.
        await API.delete(`/api/v2/${tenantSlug}/sessions/${revokeTargetId}`);
        showSuccess(
          tr('console.settings.revoke_session_success', 'Session revoked'),
        );
        fetchSessions();
      }
    } catch (_) {
      // interceptor handles toast
    } finally {
      setRevoking(false);
      setRevokeVisible(false);
      setRevokeTargetId(null);
    }
  };

  // handleRevokeOthers (L7): "sign out other devices" — revokes every
  // session of the caller except the current one and refetches the list so
  // the table reflects the new (singleton) state.
  const handleRevokeOthers = async () => {
    if (revokingOthers) return;
    setRevokingOthers(true);
    try {
      const res = await API.delete(`/api/v2/${tenantSlug}/sessions/others`);
      if (res?.data?.success) {
        const revoked = res.data.data?.revoked ?? 0;
        showSuccess(
          tr(
            'console.settings.revoke_others_success',
            '{{count}} other session(s) revoked',
            { count: revoked },
          ),
        );
        fetchSessions();
      } else {
        showError(
          res?.data?.message ||
            tr(
              'console.settings.revoke_others_failed',
              'Failed to sign out other devices',
            ),
        );
      }
    } catch (_) {
      // interceptor handles toast
    } finally {
      setRevokingOthers(false);
      setRevokeOthersVisible(false);
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

  const handleNotifyChange = (field, value) => {
    setNotifyForm((prev) => ({ ...prev, [field]: value }));
  };

  // PUT /api/user/setting — NOT tenant-scoped (api-router.go:59), same
  // endpoint PersonalSetting.jsx already writes. Field names copied
  // verbatim from UpdateUserSettingRequest (user.go:521-531); the two
  // fields this panel has no editor for (accept_unset_model_ratio_model,
  // record_ip_log) are still sent, carrying the value seeded from the
  // server, so a save here cannot silently reset them.
  const handleSaveNotify = async () => {
    if (notifySaving) return;
    setNotifySaving(true);
    try {
      const body = {
        notify_type: notifyForm.notifyType,
        quota_warning_threshold: Number(notifyForm.quotaWarningThreshold) || 0,
        webhook_url: notifyForm.webhookUrl,
        webhook_secret: notifyForm.webhookSecret,
        notification_email: notifyForm.notificationEmail,
        bark_url: notifyForm.barkUrl,
        gotify_url: notifyForm.gotifyUrl,
        gotify_token: notifyForm.gotifyToken,
        gotify_priority: parseInt(notifyForm.gotifyPriority, 10) || 0,
        accept_unset_model_ratio_model: notifyForm.acceptUnsetModelRatioModel,
        record_ip_log: notifyForm.recordIpLog,
      };
      const res = await API.put('/api/user/setting', body);
      if (res?.data?.success) {
        showSuccess(tr('console.settings.toast_saved', 'Saved'));
        fetchProfile();
      } else {
        showError(
          res?.data?.message ||
            tr('console.settings.toast_save_failed', 'Save failed'),
        );
      }
    } catch (_) {
      showError(tr('console.settings.toast_save_failed', 'Save failed'));
    } finally {
      setNotifySaving(false);
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
                  {/* The dot is green ONLY when the user is actually enrolled.
                      A pending enrolment (secret issued, never confirmed) does
                      not protect the account and must not read as protection. */}
                  <span
                    className={
                      totpStatus && totpStatus.enrolled ? 'dot ok' : 'dot'
                    }
                    data-testid='mfa-dot'
                  />{' '}
                  <span className='strong' data-testid='mfa-status'>
                    {totpLoading
                      ? tr('console.common.loading', 'loading…')
                      : totpStatus === null || totpStatus === false
                        ? tr(
                            'console.settings.mfa_status_unknown',
                            'authenticator app · status unavailable',
                          )
                        : totpStatus.enrolled
                          ? tr(
                              'console.settings.mfa_status_enrolled',
                              'authenticator app · enabled',
                            )
                          : totpStatus.pending
                            ? tr(
                                'console.settings.mfa_status_pending',
                                'authenticator app · setup started, not confirmed',
                              )
                            : tr(
                                'console.settings.mfa_status_off',
                                'authenticator app · not enabled',
                              )}
                  </span>
                  <span style={{ flex: 1 }} />
                  <button
                    type='button'
                    className='btn sm'
                    data-testid='mfa-manage'
                    onClick={() => navigate('/console/personal')}
                    title={tr(
                      'console.settings.mfa_manage_title',
                      'enrol, regenerate or disable your authenticator',
                    )}
                  >
                    {totpStatus && totpStatus.enrolled
                      ? tr('console.settings.mfa_manage', 'manage')
                      : tr('console.settings.mfa_enable', 'set up')}
                  </button>
                </div>
                {/* backup_codes_remaining only appears on the status
                    response once TOTP is enrolled — a self-service escape
                    hatch (issued once, shown once) so this panel is honest
                    about how many are left, not just whether TOTP is on. */}
                {totpStatus && totpStatus.enrolled && (
                  <div
                    className='muted mono'
                    style={{ fontSize: 11, marginTop: 8 }}
                    data-testid='mfa-backup-codes-remaining'
                  >
                    {tr(
                      'console.settings.mfa_backup_codes_remaining',
                      'backup codes remaining: {{count}}',
                      { count: totpStatus.backup_codes_remaining ?? 0 },
                    )}
                  </div>
                )}
              </div>

              <div
                className='panel'
                style={{ padding: 18, marginTop: 14, marginBottom: 14 }}
              >
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    marginBottom: 12,
                  }}
                >
                  <div className='lbl'>
                    {tr('console.settings.sessions_title', 'sessions')}
                  </div>
                  {/* L7: only worth showing once the registry proves there is
                      more than one device to sign out — with the flag off
                      (or no other device registered) there is nothing for
                      this button to do. */}
                  {!sessionLoading && sessions && sessions.length > 1 && (
                    <button
                      type='button'
                      className='btn sm'
                      data-testid='revoke-others-btn'
                      onClick={() => setRevokeOthersVisible(true)}
                    >
                      {tr(
                        'console.settings.revoke_others_btn',
                        'sign out other devices',
                      )}
                    </button>
                  )}
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
                          {tr('console.settings.th_device', 'device')}
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
                          <td className='faint' style={{ padding: '8px 8px' }}>
                            {/* ip/user_agent_family only exist once the
                                registry is enabled (L7) — the legacy
                                synthetic row has neither. */}
                            {s.ip || s.user_agent_family
                              ? [s.ip, s.user_agent_family]
                                  .filter(Boolean)
                                  .join(' · ')
                              : '—'}
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
                              data-testid={
                                s.current
                                  ? 'revoke-session-btn'
                                  : `revoke-session-btn-${s.id}`
                              }
                              onClick={() => {
                                // s.current -> the caller's own session
                                // (/sessions/current, unchanged behaviour);
                                // otherwise -> revoke-by-id (L7) for a
                                // registered device that is not this one.
                                setRevokeTargetId(s.current ? null : s.id);
                                setRevokeVisible(true);
                              }}
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

              {/* Revoke session confirm dialog — shared by "revoke my
                  current session" (revokeTargetId === null) and "revoke
                  this other device by id" (L7, revokeTargetId set) since
                  they only differ in copy and in which endpoint is called. */}
              <ConfirmDialog
                visible={revokeVisible}
                title={
                  revokeTargetId == null
                    ? tr(
                        'console.settings.confirm_revoke_title',
                        'Revoke current session',
                      )
                    : tr(
                        'console.settings.confirm_revoke_other_title',
                        'Revoke this session',
                      )
                }
                consequenceList={
                  revokeTargetId == null
                    ? [
                        tr(
                          'console.settings.confirm_revoke_c1',
                          'This browser session will be invalidated immediately.',
                        ),
                        tr(
                          'console.settings.confirm_revoke_c2',
                          'You will be redirected to the login page and must sign in again.',
                        ),
                      ]
                    : [
                        tr(
                          'console.settings.confirm_revoke_other_c1',
                          'That device will be signed out immediately.',
                        ),
                      ]
                }
                confirmText='revoke'
                confirmButtonText={tr(
                  'console.settings.revoke_session_btn',
                  'revoke session',
                )}
                confirmButtonType='danger'
                onConfirm={handleRevokeSession}
                onCancel={() => {
                  setRevokeVisible(false);
                  setRevokeTargetId(null);
                }}
              />

              {/* "Sign out other devices" confirm dialog (L7). */}
              <ConfirmDialog
                visible={revokeOthersVisible}
                title={tr(
                  'console.settings.confirm_revoke_others_title',
                  'Sign out other devices',
                )}
                consequenceList={[
                  tr(
                    'console.settings.confirm_revoke_others_c1',
                    'Every OTHER session on your account will be invalidated immediately.',
                  ),
                  tr(
                    'console.settings.confirm_revoke_others_c2',
                    'This browser stays signed in.',
                  ),
                ]}
                confirmText='revoke'
                confirmButtonText={tr(
                  'console.settings.revoke_others_btn',
                  'sign out other devices',
                )}
                confirmButtonType='danger'
                onConfirm={handleRevokeOthers}
                onCancel={() => setRevokeOthersVisible(false)}
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
                  <div
                    className='panel'
                    style={{ padding: 18, marginBottom: 14 }}
                  >
                    <div className='lbl'>
                      {tr(
                        'console.settings.routing_group_label',
                        'routing group',
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
                        <span data-testid='subscription-group'>
                          {subData.group ||
                            tr(
                              'console.settings.group_none',
                              'no group assigned',
                            )}
                        </span>
                      </span>
                      <span className='faint mono' style={{ fontSize: 11 }}>
                        {tr(
                          'console.settings.group_source_user_group',
                          "from your account's routing group",
                        )}
                      </span>
                      <span style={{ flex: 1 }} />
                      <button
                        type='button'
                        className='btn sm'
                        disabled
                        data-testid='subscription-upgrade-btn'
                        title={tr(
                          'console.settings.upgrade_plan_title',
                          'Contact your administrator',
                        )}
                      >
                        {tr('console.settings.upgrade_request', 'Upgrade')}
                      </button>
                    </div>
                  </div>

                  <div className='panel'>
                    <div
                      data-testid='subscription-entitlements-unpublished'
                      style={{ padding: '12px 16px', fontSize: 13 }}
                    >
                      {tr(
                        'console.settings.entitlements_not_published',
                        'SLA, audit retention and support terms are not published by this deployment',
                      )}
                    </div>
                  </div>
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

          {/* ── Notifications (2026-09-16) ──
              Real subscription against the store PersonalSetting.jsx already
              writes: PUT /api/user/setting (user.go:521-535), dispatched on
              the live relay consumption path by app.NotifyUser
              (internal/app/user_notify.go:53) via checkAndSendQuotaNotify
              (internal/app/quota.go:1390). */}
          {section === 'notifications' && (
            <div style={{ marginTop: 22 }} data-testid='notifications-section'>
              <div className='panel' style={{ padding: 18, marginTop: 8 }}>
                <div className='lbl' style={{ marginBottom: 8 }}>
                  {tr('console.settings.notify_type_label', 'alert via')}
                </div>
                <select
                  data-testid='notify-type-select'
                  style={inputStyle}
                  value={notifyForm.notifyType}
                  onChange={(e) =>
                    handleNotifyChange('notifyType', e.target.value)
                  }
                >
                  <option value='email'>
                    {tr('console.settings.notify_type_email', 'email')}
                  </option>
                  <option value='webhook'>
                    {tr('console.settings.notify_type_webhook', 'webhook')}
                  </option>
                  <option value='bark'>
                    {tr('console.settings.notify_type_bark', 'bark')}
                  </option>
                  <option value='gotify'>
                    {tr('console.settings.notify_type_gotify', 'gotify')}
                  </option>
                </select>

                <div className='lbl' style={{ marginTop: 16, marginBottom: 8 }}>
                  {tr(
                    'console.settings.notify_threshold_label',
                    'quota warning threshold',
                  )}
                </div>
                <input
                  type='number'
                  data-testid='notify-threshold-input'
                  style={inputStyle}
                  value={notifyForm.quotaWarningThreshold}
                  onChange={(e) =>
                    handleNotifyChange('quotaWarningThreshold', e.target.value)
                  }
                />

                {notifyForm.notifyType === 'email' && (
                  <>
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_email_label',
                        'notification email (optional — defaults to account email)',
                      )}
                    </div>
                    <input
                      data-testid='notify-email-input'
                      style={inputStyle}
                      value={notifyForm.notificationEmail}
                      onChange={(e) =>
                        handleNotifyChange('notificationEmail', e.target.value)
                      }
                    />
                  </>
                )}

                {notifyForm.notifyType === 'webhook' && (
                  <>
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_webhook_url_label',
                        'webhook url',
                      )}
                    </div>
                    <input
                      data-testid='notify-webhook-url-input'
                      style={inputStyle}
                      value={notifyForm.webhookUrl}
                      onChange={(e) =>
                        handleNotifyChange('webhookUrl', e.target.value)
                      }
                    />
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_webhook_secret_label',
                        'webhook secret',
                      )}
                    </div>
                    <input
                      data-testid='notify-webhook-secret-input'
                      style={inputStyle}
                      value={notifyForm.webhookSecret}
                      onChange={(e) =>
                        handleNotifyChange('webhookSecret', e.target.value)
                      }
                    />
                  </>
                )}

                {notifyForm.notifyType === 'bark' && (
                  <>
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_bark_url_label',
                        'bark push url',
                      )}
                    </div>
                    <input
                      data-testid='notify-bark-url-input'
                      style={inputStyle}
                      value={notifyForm.barkUrl}
                      onChange={(e) =>
                        handleNotifyChange('barkUrl', e.target.value)
                      }
                    />
                  </>
                )}

                {notifyForm.notifyType === 'gotify' && (
                  <>
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_gotify_url_label',
                        'gotify server url',
                      )}
                    </div>
                    <input
                      data-testid='notify-gotify-url-input'
                      style={inputStyle}
                      value={notifyForm.gotifyUrl}
                      onChange={(e) =>
                        handleNotifyChange('gotifyUrl', e.target.value)
                      }
                    />
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_gotify_token_label',
                        'gotify app token',
                      )}
                    </div>
                    <input
                      data-testid='notify-gotify-token-input'
                      style={inputStyle}
                      value={notifyForm.gotifyToken}
                      onChange={(e) =>
                        handleNotifyChange('gotifyToken', e.target.value)
                      }
                    />
                    <div
                      className='lbl'
                      style={{ marginTop: 16, marginBottom: 8 }}
                    >
                      {tr(
                        'console.settings.notify_gotify_priority_label',
                        'gotify priority (0-10)',
                      )}
                    </div>
                    <input
                      type='number'
                      data-testid='notify-gotify-priority-input'
                      style={inputStyle}
                      value={notifyForm.gotifyPriority}
                      onChange={(e) =>
                        handleNotifyChange('gotifyPriority', e.target.value)
                      }
                    />
                  </>
                )}

                <button
                  type='button'
                  className='btn sm'
                  style={{ marginTop: 18 }}
                  data-testid='notify-save-btn'
                  disabled={notifySaving}
                  onClick={handleSaveNotify}
                >
                  {tr('console.common.save', 'save')}
                </button>
              </div>
            </div>
          )}

          {/* ── Team ──
              Retired as a newhub feature (2026-09-16): account and
              membership lifecycle — invites, roles, removal — belongs to
              the platform identity service. This service reads tenant
              membership, it does not own it (repo CLAUDE.md, "Auth"). No
              outbound link: identity.lurus.cn's authenticated customer nav
              is wallet/topup/subscriptions/invoices/refunds/redeem/account/
              data-privacy — no team or member screen a customer can reach.
              The only org-membership surface there is an internal
              /admin/v1 tool, not customer self-service. Per-tenant team
              management is simply not offered in this product yet — this
              section says that plainly instead of linking somewhere that
              does not serve the capability. */}
          {section === 'team' && (
            <div style={{ marginTop: 22 }} data-testid='team-section'>
              <div className='panel' style={{ padding: 18, marginTop: 8 }}>
                <div
                  className='muted'
                  style={{ fontSize: 12, lineHeight: 1.6 }}
                >
                  {tr(
                    'console.settings.team_not_available_desc',
                    'Per-tenant team management — invites, roles, removal — is not available in this product yet.',
                  )}
                </div>
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
