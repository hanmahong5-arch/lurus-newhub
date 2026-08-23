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
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import {
  QUOTA_PER_USD,
  quotaToUSD,
  formatRelativeTime,
} from '../../../helpers/formatting';

const RELAY_HOST = 'https://api.lurus.cn';
const BASE_URL = `${RELAY_HOST}/v1`;

// Client base URLs the hub genuinely speaks. We intentionally omit an "Azure"
// URL: the hub's client-facing relay is OpenAI-compatible at /v1 (Azure is an
// upstream channel type, not a client endpoint) — inventing one would be a
// fabricated URL. Each entry is copy-pasteable into the matching SDK's baseURL.
const CLIENT_ENDPOINTS = [
  ['OpenAI / compatible', `${RELAY_HOST}/v1`],
  ['Anthropic · Claude SDK', RELAY_HOST],
  ['Gemini', `${RELAY_HOST}/v1beta`],
];

// Quota bar colour keyed to REMAINING headroom: red < 10%, amber < 30%, else
// green. Unlimited tokens have no cap → neutral.
const quotaBarColor = (remainingRatio) => {
  if (remainingRatio == null) return 'var(--hf-ink-2)';
  if (remainingRatio < 0.1) return 'var(--hf-err)';
  if (remainingRatio < 0.3) return 'var(--hf-warn)';
  return 'var(--hf-ok)';
};

const tokenStatus = (t) => {
  if (!t || t.status !== 1) return 'disabled';
  if (t.expired_time > 0 && t.expired_time < Math.floor(Date.now() / 1000))
    return 'expired';
  if (!t.unlimited_quota) {
    const total = t.used_quota + t.remain_quota;
    if (t.remain_quota <= 0 || (total > 0 && t.used_quota / total >= 0.9))
      return 'near-cap';
  }
  return 'live';
};

const maskKey = (key) => (key ? `sk-...${key.slice(-4)}` : 'sk-...????');

const fmtExpiry = (ts) => {
  if (!ts || ts === -1) return 'never';
  return new Date(ts * 1000).toLocaleDateString();
};

const fmtModels = (t) =>
  t.model_limits_enabled && t.model_limits ? t.model_limits : 'all models';

const fmtIPs = (ip) => (!ip ? 'any' : ip);

const copy = async (text) => {
  try {
    await navigator.clipboard.writeText(text);
    showSuccess('Copied');
  } catch (_) {
    showError('Copy failed');
  }
};

const buildSnippets = (key) => ({
  curl: `curl ${BASE_URL}/chat/completions \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'`,
  python: `from openai import OpenAI

client = OpenAI(api_key="${key}", base_url="${BASE_URL}")
resp = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "hi"}],
)
print(resp.choices[0].message.content)`,
  node: `import OpenAI from "openai";

const client = new OpenAI({ apiKey: "${key}", baseURL: "${BASE_URL}" });
const r = await client.chat.completions.create({
  model: "gpt-4o",
  messages: [{ role: "user", content: "hi" }],
});`,
  anthropic: `import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: "${key}",
  baseURL: "https://api.lurus.cn",
});
await client.messages.create({
  model: "claude-3.5-sonnet",
  max_tokens: 1024,
  messages: [{ role: "user", content: "hi" }],
});`,
});

// ─── Create token modal ───────────────────────────────────────────────────────

const CreateModal = ({ tenantSlug, onCreated, onClose }) => {
  const { t: tr } = useTranslation();
  const [form, setForm] = useState({
    name: '',
    cap: '',
    unlimited: true,
    models: '',
    limitModels: false,
    // Per-token rate limits. JSON keys mirror entity/token.go tags
    // (rate_limit_rpm / rate_limit_tpm); 0 = unlimited.
    rpm: '',
    tpm: '',
  });
  const [saving, setSaving] = useState(false);
  const nameRef = useRef(null);

  useEffect(() => {
    nameRef.current?.focus();
  }, []);

  const submit = async (e) => {
    e.preventDefault();
    if (!form.name.trim()) return;
    setSaving(true);
    try {
      const capUSD = parseFloat(form.cap) || 0;
      const res = await API.post(`/api/v2/${tenantSlug}/tokens`, {
        name: form.name.trim(),
        unlimited_quota: form.unlimited || capUSD <= 0,
        remain_quota:
          form.unlimited || capUSD <= 0
            ? 0
            : Math.round(capUSD * QUOTA_PER_USD),
        model_limits_enabled: form.limitModels && !!form.models.trim(),
        model_limits: form.models.trim(),
        expired_time: -1,
        rate_limit_rpm: Math.max(0, parseInt(form.rpm, 10) || 0),
        rate_limit_tpm: Math.max(0, parseInt(form.tpm, 10) || 0),
      });
      if (res?.data?.success) {
        const { key } = res.data.data;
        showSuccess(
          tr(
            'console.token.toast_created',
            "Token created — copy your key now, it won't be shown again.",
          ),
        );
        onCreated(key);
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
          width: 400,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div className='strong' style={{ fontSize: 15 }}>
          {tr('console.token.modal_title', 'New token')}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span className='lbl'>
            {tr('console.token.field_name', 'name *')}
          </span>
          <input
            ref={nameRef}
            style={{
              fontFamily: 'var(--hf-mono)',
              fontSize: 12,
              padding: '5px 8px',
              border: '1px solid var(--hf-rule)',
              background: 'var(--hf-sunken)',
              color: 'var(--hf-ink)',
              borderRadius: 2,
              outline: 'none',
              width: '100%',
            }}
            placeholder={tr('console.token.ph_name', 'e.g. prod-backend')}
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
        </label>

        <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <input
            type='checkbox'
            checked={form.unlimited}
            onChange={(e) =>
              setForm((f) => ({ ...f, unlimited: e.target.checked }))
            }
          />
          <span className='lbl'>
            {tr('console.token.unlimited_quota', 'unlimited quota')}
          </span>
        </label>

        {!form.unlimited && (
          <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <span className='lbl'>
              {tr('console.token.monthly_cap_usd', 'monthly cap ($)')}
            </span>
            <input
              style={{
                fontFamily: 'var(--hf-mono)',
                fontSize: 12,
                padding: '5px 8px',
                border: '1px solid var(--hf-rule)',
                background: 'var(--hf-sunken)',
                color: 'var(--hf-ink)',
                borderRadius: 2,
                outline: 'none',
                width: '100%',
              }}
              type='number'
              min='0'
              step='0.01'
              placeholder='200'
              value={form.cap}
              onChange={(e) => setForm((f) => ({ ...f, cap: e.target.value }))}
            />
          </label>
        )}

        <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <input
            type='checkbox'
            checked={form.limitModels}
            onChange={(e) =>
              setForm((f) => ({ ...f, limitModels: e.target.checked }))
            }
          />
          <span className='lbl'>
            {tr('console.token.restrict_models', 'restrict models')}
          </span>
        </label>

        {form.limitModels && (
          <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <span className='lbl'>
              {tr(
                'console.token.allowed_models',
                'allowed models (comma-separated)',
              )}
            </span>
            <input
              style={{
                fontFamily: 'var(--hf-mono)',
                fontSize: 12,
                padding: '5px 8px',
                border: '1px solid var(--hf-rule)',
                background: 'var(--hf-sunken)',
                color: 'var(--hf-ink)',
                borderRadius: 2,
                outline: 'none',
                width: '100%',
              }}
              placeholder={tr(
                'console.token.ph_models',
                'gpt-4o, claude-3.5-sonnet',
              )}
              value={form.models}
              onChange={(e) =>
                setForm((f) => ({ ...f, models: e.target.value }))
              }
            />
          </label>
        )}

        {/* Per-token rate limits — 0 / empty = unlimited. */}
        <div
          style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}
        >
          {[
            ['rpm', tr('console.token.field_rpm', 'rpm limit')],
            ['tpm', tr('console.token.field_tpm', 'tpm limit')],
          ].map(([k, label]) => (
            <label
              key={k}
              style={{ display: 'flex', flexDirection: 'column', gap: 5 }}
            >
              <span className='lbl'>{label}</span>
              <input
                style={{
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 12,
                  padding: '5px 8px',
                  border: '1px solid var(--hf-rule)',
                  background: 'var(--hf-sunken)',
                  color: 'var(--hf-ink)',
                  borderRadius: 2,
                  outline: 'none',
                  width: '100%',
                }}
                type='number'
                min='0'
                step='1'
                placeholder={tr('console.token.ph_rate_limit', '0 = unlimited')}
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
              ? tr('console.token.creating', 'creating…')
              : tr('console.token.create_token', 'create token')}
          </button>
        </div>
      </form>
    </div>
  );
};

// ─── Inline setting editor ────────────────────────────────────────────────────

const InlineEdit = ({ value, onSave, onCancel }) => {
  const [v, setV] = useState(value);
  const ref = useRef(null);

  useEffect(() => {
    ref.current?.select();
  }, []);

  const commit = () => {
    if (v.trim() !== value) onSave(v.trim());
    else onCancel();
  };

  return (
    <input
      ref={ref}
      style={{
        fontFamily: 'var(--hf-mono)',
        fontSize: 12,
        padding: '3px 6px',
        width: '100%',
        border: '1px solid var(--hf-rule)',
        background: 'var(--hf-sunken)',
        color: 'var(--hf-ink)',
        borderRadius: 2,
        outline: 'none',
      }}
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

// ─── Main page ────────────────────────────────────────────────────────────────

const LANG_TABS = [
  ['curl', 'cURL'],
  ['python', 'Python'],
  ['node', 'Node.js'],
  ['anthropic', 'Anthropic SDK'],
];

const HFToken = () => {
  const navigate = useNavigate();
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` because this page uses `t` as the token loop variable in
  // several .map/.filter callbacks — `t` would otherwise shadow the translator.
  const { t: tr } = useTranslation();

  const [tokens, setTokens] = useState([]);
  const [loading, setLoading] = useState(true);
  const [sel, setSel] = useState(0);
  const [lang, setLang] = useState('curl');
  const [revealed, setRevealed] = useState(new Set());
  const [rotatedKeys, setRotatedKeys] = useState({}); // id → new sk-xxx key
  const [creating, setCreating] = useState(false);
  const [newlyCreatedKey, setNewlyCreatedKey] = useState(null);
  const [editField, setEditField] = useState(null); // 'models'|'cap'|'expires'|'ips'
  const [saving, setSaving] = useState(false);
  // Tier 1.3: confirm dialog state for rotate / revoke. `intent` is the
  // verb so a single dialog handles both flows without duplicating layout.
  const [confirmIntent, setConfirmIntent] = useState(null); // 'rotate' | 'revoke' | null
  // Phase 2b batch ops: `marked` is the id-based multi-select Set (kept separate
  // from the master-detail index `sel` so the two never collide).
  const [marked, setMarked] = useState(new Set());
  const [confirmBatchDelete, setConfirmBatchDelete] = useState(false);

  const fetchTokens = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/tokens?p=1&size=100`);
      if (res?.data?.success) {
        setTokens(res.data.data.items ?? []);
        setSel(0);
      }
    } catch (_) {
    } finally {
      setLoading(false);
    }
  }, [tenantSlug]);

  useEffect(() => {
    if (tenantSlug) fetchTokens();
  }, [fetchTokens, tenantSlug]);

  const token = tokens[sel];

  // ── Computed summary ──────────────────────────────────────────────────────

  const activeCount = tokens.filter((t) => tokenStatus(t) === 'live').length;
  const totalUsedUSD = tokens
    .reduce((s, t) => s + t.used_quota / QUOTA_PER_USD, 0)
    .toFixed(2);
  const totalCapUSD = tokens
    .filter((t) => !t.unlimited_quota)
    .reduce((s, t) => s + (t.used_quota + t.remain_quota) / QUOTA_PER_USD, 0)
    .toFixed(2);

  // ── Per-token derived values ──────────────────────────────────────────────

  // Full plaintext key is available only transiently right after rotation
  // (rotatedKeys). For existing tokens the backend returns a masked key only —
  // the plaintext is shown exactly once, on create or rotate.
  const rotatedKey = (t) => (t && rotatedKeys[t.id]) || null;

  const displayKey = (t) => {
    if (!t) return 'sk-...????';
    const full = rotatedKey(t);
    if (full && revealed.has(t.id)) return full;
    return maskKey(t.key);
  };

  // ── Actions ───────────────────────────────────────────────────────────────

  const handleReveal = (t) => {
    setRevealed((prev) => {
      const next = new Set(prev);
      if (next.has(t.id)) next.delete(t.id);
      else next.add(t.id);
      return next;
    });
  };

  const toggleMark = (id) => {
    setMarked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const performBatchDelete = async () => {
    if (marked.size === 0) return;
    setSaving(true);
    try {
      const ids = [...marked];
      const res = await API.post(`/api/v2/${tenantSlug}/tokens/batch-delete`, {
        ids,
      });
      if (res?.data?.success) {
        showSuccess(
          tr('console.token.toast_deleted', {
            count: res.data.deleted ?? ids.length,
          }),
        );
        setConfirmBatchDelete(false);
        setMarked(new Set());
        await fetchTokens();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  // Tier 1.3: rotate / revoke route through ConfirmDialog. The handlers
  // below open the dialog; the actual mutation happens in performRotate /
  // performRevoke once the user types the token name and clicks Confirm.
  const handleRotate = () => {
    if (!token) return;
    setConfirmIntent('rotate');
  };

  const handleRevoke = () => {
    if (!token) return;
    setConfirmIntent('revoke');
  };

  const performRotate = async () => {
    setSaving(true);
    try {
      const res = await API.post(
        `/api/v2/${tenantSlug}/tokens/${token.id}/rotate`,
        {},
        { skipErrorHandler: false },
      );
      if (res?.data?.success) {
        const newKey = res.data.data.key;
        setRotatedKeys((prev) => ({ ...prev, [token.id]: newKey }));
        setRevealed((prev) => new Set([...prev, token.id]));
        showSuccess(
          tr(
            'console.token.toast_rotated',
            'Key rotated — copy the new key now.',
          ),
        );
        setConfirmIntent(null);
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  const performRevoke = async () => {
    setSaving(true);
    try {
      const res = await API.delete(`/api/v2/${tenantSlug}/tokens/${token.id}`);
      if (res?.data?.success) {
        showSuccess(tr('console.token.toast_revoked', 'Token revoked'));
        setConfirmIntent(null);
        await fetchTokens();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  const handleSaveField = async (field, rawValue) => {
    if (!token) return;
    setEditField(null);
    const body = {};
    if (field === 'models') {
      const v = rawValue.trim();
      body.model_limits_enabled = v !== '' && v !== 'all models';
      body.model_limits = body.model_limits_enabled ? v : '';
    } else if (field === 'cap') {
      const usd = parseFloat(rawValue) || 0;
      if (usd <= 0) {
        body.unlimited_quota = true;
        body.remain_quota = 0;
      } else {
        const newTotalUnits = Math.round(usd * QUOTA_PER_USD);
        body.unlimited_quota = false;
        body.remain_quota = Math.max(0, newTotalUnits - token.used_quota);
      }
    } else if (field === 'expires') {
      body.expired_time =
        rawValue === 'never' || rawValue === ''
          ? -1
          : Math.floor(new Date(rawValue).getTime() / 1000);
    } else if (field === 'ips') {
      body.allow_ips = rawValue === 'any' ? '' : rawValue;
    } else if (field === 'rpm' || field === 'tpm') {
      // JSON keys mirror entity/token.go tags; 0 = unlimited ('∞' parses
      // to NaN → 0, so clearing back to unlimited just works).
      body[field === 'rpm' ? 'rate_limit_rpm' : 'rate_limit_tpm'] = Math.max(
        0,
        parseInt(rawValue, 10) || 0,
      );
    }
    if (Object.keys(body).length === 0) return;
    setSaving(true);
    try {
      const res = await API.put(
        `/api/v2/${tenantSlug}/tokens/${token.id}`,
        body,
      );
      if (res?.data?.success) {
        showSuccess(tr('console.token.toast_saved', 'Saved'));
        await fetchTokens();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  const handleCreated = async (key) => {
    setCreating(false);
    setNewlyCreatedKey(key);
    await fetchTokens();
  };

  // ── Render helpers ────────────────────────────────────────────────────────

  const statusClass = (st) =>
    st === 'live' ? 'tag ok' : st === 'near-cap' ? 'tag warn' : 'tag';

  const capDisplay = (t) => {
    if (t.unlimited_quota) return '∞';
    const total = t.used_quota + t.remain_quota;
    return `$${quotaToUSD(t.used_quota)} / $${quotaToUSD(total)}`;
  };

  const capRatio = (t) => {
    if (t.unlimited_quota) return 0;
    const total = t.used_quota + t.remain_quota;
    return total > 0 ? t.used_quota / total : 0;
  };

  const settingsRows = token
    ? [
        [
          tr('console.token.model_scope', 'model scope'),
          fmtModels(token),
          'models',
        ],
        [
          tr('console.token.monthly_cap', 'monthly cap'),
          token.unlimited_quota
            ? '∞'
            : `$${quotaToUSD(token.used_quota + token.remain_quota)}`,
          'cap',
        ],
        [
          tr('console.token.expires', 'expires'),
          fmtExpiry(token.expired_time),
          'expires',
        ],
        [
          tr('console.token.allowed_ips', 'allowed ips'),
          fmtIPs(token.allow_ips),
          'ips',
        ],
        [
          tr('console.token.rate_limit_rpm', 'rate limit · rpm'),
          token.rate_limit_rpm > 0 ? String(token.rate_limit_rpm) : '∞',
          'rpm',
        ],
        [
          tr('console.token.rate_limit_tpm', 'rate limit · tpm'),
          token.rate_limit_tpm > 0 ? String(token.rate_limit_tpm) : '∞',
          'tpm',
        ],
      ]
    : [];

  const snippetMap = buildSnippets(rotatedKey(token) || 'YOUR_KEY');

  // ─────────────────────────────────────────────────────────────────────────

  return (
    <HFShell
      active='tokens'
      crumbs={[
        tr('console.nav.section_my_account', 'my account'),
        tr('console.token.crumb', 'tokens'),
      ]}
      actions={
        <>
          {loading ? (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.common.loading', 'loading…')}
            </span>
          ) : (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.token.summary', {
                active: activeCount,
                used: totalUsedUSD,
              })}
              {parseFloat(totalCapUSD) > 0 ? ` / $${totalCapUSD}` : ''}
            </span>
          )}
          <button
            type='button'
            className='btn primary'
            onClick={() => setCreating(true)}
          >
            {tr('console.token.new_token', '+ new token')}
          </button>
        </>
      }
    >
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '380px 1fr',
          minHeight: 0,
          height: '100%',
        }}
      >
        {/* ── Left: token list ── */}
        <div
          style={{
            borderRight: '1px solid var(--hf-rule)',
            overflow: 'auto',
            background: 'var(--hf-paper)',
          }}
        >
          <div
            style={{
              padding: '20px 22px',
              borderBottom: '1px solid var(--hf-rule)',
            }}
          >
            <div className='lbl' style={{ marginBottom: 4 }}>
              {tr('console.token.your_tokens', 'your tokens')}
            </div>
            <div className='display' style={{ fontSize: 26 }}>
              {loading
                ? '…'
                : tr('console.token.count', {
                    count: tokens.length,
                  })}
            </div>
          </div>

          {/* Batch action bar — appears once any token is selected. */}
          {marked.size > 0 && (
            <div
              data-testid='token-batch-bar'
              style={{
                padding: '10px 22px',
                borderBottom: '1px solid var(--hf-rule)',
                background: 'var(--hf-accent)',
                color: '#fff',
                fontFamily: 'var(--hf-mono)',
                fontSize: 11,
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                flexWrap: 'wrap',
              }}
            >
              <span>
                <b>{marked.size}</b> {tr('console.token.selected', 'selected')}
              </span>
              <span style={{ flex: 1 }} />
              {/* Batch copy is honestly deferred: keys are masked in the list, so
                  revealing N plaintext keys at once needs a key-reveal endpoint
                  (security review). Greyed with a reason, never silently absent. */}
              <button
                type='button'
                className='btn'
                data-testid='token-batch-copy-btn'
                disabled
                title={tr(
                  'console.token.batch_copy_deferred',
                  'batch copy needs a key-reveal endpoint — deferred (security review)',
                )}
                style={{
                  background: 'rgba(255,255,255,0.15)',
                  borderColor: 'rgba(255,255,255,0.3)',
                  color: '#fff',
                }}
              >
                {tr('console.common.copy', 'copy')}
              </button>
              <button
                type='button'
                className='btn'
                data-testid='token-batch-delete-btn'
                disabled={saving}
                onClick={() => setConfirmBatchDelete(true)}
                style={{
                  background: 'rgba(255,255,255,0.15)',
                  borderColor: 'rgba(255,255,255,0.3)',
                  color: '#fff',
                }}
              >
                {tr('console.common.delete', 'delete')}
              </button>
              <button
                type='button'
                className='btn ghost'
                style={{ color: '#fff' }}
                onClick={() => setMarked(new Set())}
              >
                {tr('console.token.clear', 'clear')}
              </button>
            </div>
          )}

          {loading && (
            <div
              className='muted'
              style={{ padding: '20px 22px', fontSize: 12 }}
            >
              {tr('console.common.loading', 'Loading…')}
            </div>
          )}

          {!loading && tokens.length === 0 && (
            <div
              className='muted'
              style={{ padding: '20px 22px', fontSize: 12 }}
            >
              {tr(
                'console.token.empty',
                'No tokens yet. Create one to get started.',
              )}
            </div>
          )}

          {tokens.map((t, i) => {
            const st = tokenStatus(t);
            const ratio = capRatio(t);
            return (
              <div
                key={t.id}
                onClick={() => setSel(i)}
                style={{
                  padding: '14px 22px',
                  borderBottom: '1px solid var(--hf-rule)',
                  cursor: 'pointer',
                  background: sel === i ? 'var(--hf-elev)' : 'transparent',
                  borderLeft:
                    sel === i
                      ? '2px solid var(--hf-accent)'
                      : '2px solid transparent',
                }}
              >
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 8,
                  }}
                >
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 8,
                      minWidth: 0,
                    }}
                  >
                    <input
                      type='checkbox'
                      data-testid={`token-check-${t.id}`}
                      checked={marked.has(t.id)}
                      onClick={(e) => e.stopPropagation()}
                      onChange={() => toggleMark(t.id)}
                    />
                    <span
                      className='strong'
                      style={{
                        fontSize: 13,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {t.name}
                    </span>
                  </div>
                  <span className={statusClass(st)}>
                    {tr(`console.token.status_${st}`, st)}
                  </span>
                </div>
                <div
                  className='mono muted'
                  style={{ fontSize: 10, marginTop: 4 }}
                >
                  {maskKey(t.key)}
                </div>
                <div
                  className='mono faint'
                  style={{ fontSize: 9, marginTop: 2 }}
                >
                  {tr('console.token.created', 'created')}{' '}
                  {formatRelativeTime(t.created_time)} ·{' '}
                  {tr('console.token.last_used', 'last used')}{' '}
                  {formatRelativeTime(t.accessed_time)}
                  {t.creator_user_id > 0 && (
                    <>
                      {' '}
                      · {tr('console.token.by_user', 'by user')} #
                      {t.creator_user_id}
                    </>
                  )}
                </div>
                <div className='muted' style={{ fontSize: 11, marginTop: 2 }}>
                  {fmtModels(t)}
                </div>
                {!t.unlimited_quota && t.used_quota + t.remain_quota > 0 && (
                  <div style={{ marginTop: 8 }}>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        fontSize: 10,
                        marginBottom: 3,
                      }}
                    >
                      <span className='mono muted'>{capDisplay(t)}</span>
                      <span
                        className={'mono ' + (ratio > 0.9 ? 'acc' : 'muted')}
                      >
                        {(ratio * 100).toFixed(0)}%
                      </span>
                    </div>
                    <div style={{ height: 3, background: 'var(--hf-sunken)' }}>
                      <div
                        style={{
                          height: '100%',
                          width: `${Math.min(ratio * 100, 100)}%`,
                          background: quotaBarColor(1 - ratio),
                        }}
                      />
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>

        {/* ── Right: token detail ── */}
        <div style={{ overflow: 'auto', padding: 28 }}>
          {!token && !loading && (
            <div className='muted' style={{ fontSize: 13 }}>
              {tr(
                'console.token.detail_empty',
                'Create a token to get started.',
              )}
            </div>
          )}

          {token && (
            <>
              {newlyCreatedKey && (
                <div
                  className='panel'
                  style={{
                    marginBottom: 20,
                    padding: '12px 16px',
                    border: '1px solid var(--hf-ok)',
                    background: 'rgba(31,122,79,0.06)',
                  }}
                >
                  <div
                    className='strong'
                    style={{
                      fontSize: 12,
                      marginBottom: 6,
                      color: 'var(--hf-ok)',
                    }}
                  >
                    {tr(
                      'console.token.created_banner',
                      'Token created — copy your key now',
                    )}
                  </div>
                  <div
                    style={{ display: 'flex', alignItems: 'center', gap: 10 }}
                  >
                    <code
                      className='mono'
                      style={{ fontSize: 12, flex: 1, wordBreak: 'break-all' }}
                    >
                      {newlyCreatedKey}
                    </code>
                    <button
                      type='button'
                      className='btn sm'
                      onClick={() => copy(newlyCreatedKey)}
                    >
                      {tr('console.common.copy', 'copy')}
                    </button>
                    <button
                      type='button'
                      className='btn ghost sm'
                      onClick={() => setNewlyCreatedKey(null)}
                    >
                      ✕
                    </button>
                  </div>
                </div>
              )}

              <div className='lbl' style={{ marginBottom: 4 }}>
                {tr('console.token.integration', 'integration')} · {token.name}
              </div>
              <h1
                className='display'
                style={{ fontSize: 36, margin: 0, letterSpacing: '-0.025em' }}
              >
                {tr('console.token.drop_in', 'Drop into your stack')}
              </h1>
              <div className='muted' style={{ marginTop: 6 }}>
                {tr(
                  'console.token.openai_compatible',
                  'OpenAI-compatible. Same SDK. Swap the base URL.',
                )}
              </div>

              <div className='panel' style={{ marginTop: 22, padding: 16 }}>
                <div
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '110px 1fr auto',
                    gap: 14,
                    alignItems: 'center',
                  }}
                >
                  <span className='lbl'>
                    {tr('console.token.base_url', 'base url')}
                  </span>
                  <span className='mono strong' style={{ fontSize: 13 }}>
                    {BASE_URL}
                  </span>
                  <button
                    type='button'
                    className='btn sm'
                    onClick={() => copy(BASE_URL)}
                  >
                    {tr('console.common.copy', 'copy')}
                  </button>
                </div>
                <hr
                  style={{
                    border: 0,
                    borderTop: '1px dashed var(--hf-rule)',
                    margin: '12px 0',
                  }}
                />
                <div
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '110px 1fr auto auto',
                    gap: 14,
                    alignItems: 'center',
                  }}
                >
                  <span className='lbl'>
                    {tr('console.token.api_key', 'api key')}
                  </span>
                  <span
                    className='mono strong'
                    style={{ fontSize: 13, wordBreak: 'break-all' }}
                  >
                    {displayKey(token)}
                  </span>
                  {rotatedKey(token) ? (
                    <>
                      <button
                        type='button'
                        className='btn sm'
                        onClick={() => handleReveal(token)}
                      >
                        {revealed.has(token.id)
                          ? tr('console.token.hide', 'hide')
                          : tr('console.token.reveal', 'reveal')}
                      </button>
                      <button
                        type='button'
                        className='btn sm'
                        onClick={() => copy(rotatedKey(token))}
                      >
                        {tr('console.common.copy', 'copy')}
                      </button>
                    </>
                  ) : (
                    <span
                      className='faint'
                      style={{ fontSize: 10, gridColumn: '3 / span 2' }}
                    >
                      {tr(
                        'console.token.key_shown_once',
                        'full key shown once · on create / rotate',
                      )}
                    </span>
                  )}
                </div>
              </div>

              {/* Client base URLs — copy the right baseURL per SDK. */}
              <div className='panel' style={{ marginTop: 14, padding: 16 }}>
                <div className='lbl' style={{ marginBottom: 10 }}>
                  {tr('console.token.client_base_urls', 'client base urls')}
                </div>
                <div
                  style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
                >
                  {CLIENT_ENDPOINTS.map(([label, url]) => (
                    <div
                      key={label}
                      style={{
                        display: 'grid',
                        gridTemplateColumns: '170px 1fr auto',
                        gap: 12,
                        alignItems: 'center',
                      }}
                    >
                      <span className='lbl'>{label}</span>
                      <span
                        className='mono'
                        style={{ fontSize: 12, wordBreak: 'break-all' }}
                      >
                        {url}
                      </span>
                      <button
                        type='button'
                        className='btn ghost sm'
                        data-testid={`copy-endpoint-${label}`}
                        onClick={() => copy(url)}
                      >
                        {tr('console.common.copy', 'copy')}
                      </button>
                    </div>
                  ))}
                </div>
              </div>

              {/* Code snippets */}
              <div
                style={{
                  display: 'flex',
                  gap: 0,
                  marginTop: 22,
                  borderBottom: '1px solid var(--hf-rule)',
                }}
              >
                {LANG_TABS.map(([k, l]) => (
                  <button
                    key={k}
                    type='button'
                    onClick={() => setLang(k)}
                    style={{
                      padding: '10px 16px',
                      border: 0,
                      background: 'transparent',
                      cursor: 'pointer',
                      fontFamily: 'var(--hf-mono)',
                      fontSize: 11,
                      color: lang === k ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
                      borderBottom:
                        lang === k
                          ? '2px solid var(--hf-accent)'
                          : '2px solid transparent',
                      marginBottom: -1,
                    }}
                  >
                    {l}
                  </button>
                ))}
                <span style={{ flex: 1 }} />
                <button
                  type='button'
                  className='btn ghost sm'
                  style={{ alignSelf: 'center' }}
                  onClick={() => copy(snippetMap[lang])}
                >
                  {tr('console.common.copy', 'copy')} ⧉
                </button>
              </div>
              <pre
                className='mono'
                style={{
                  margin: 0,
                  padding: 18,
                  fontSize: 11,
                  background: 'var(--hf-paper)',
                  border: '1px solid var(--hf-rule)',
                  borderTop: 0,
                  color: 'var(--hf-ink-2)',
                  whiteSpace: 'pre',
                  overflow: 'auto',
                }}
              >
                {snippetMap[lang]}
              </pre>

              {/* Settings */}
              <div className='lbl' style={{ marginTop: 24, marginBottom: 8 }}>
                {tr('console.token.token_settings', 'token settings')}
              </div>
              <div className='panel'>
                {settingsRows.map(([label, value, field], i, arr) => (
                  <div
                    key={field}
                    style={{
                      display: 'grid',
                      gridTemplateColumns: '160px 1fr auto',
                      padding: '12px 16px',
                      borderBottom:
                        i < arr.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                      fontSize: 12,
                      alignItems: 'center',
                    }}
                  >
                    <span className='lbl' style={{ alignSelf: 'center' }}>
                      {label}
                    </span>
                    {editField === field ? (
                      <InlineEdit
                        value={value}
                        onSave={(v) => handleSaveField(field, v)}
                        onCancel={() => setEditField(null)}
                      />
                    ) : (
                      <span className='strong'>{value}</span>
                    )}
                    {editField !== field && (
                      <button
                        type='button'
                        className='btn ghost sm'
                        disabled={saving}
                        onClick={() => setEditField(field)}
                      >
                        {tr('console.common.edit', 'edit')}
                      </button>
                    )}
                  </div>
                ))}
              </div>

              {/* Actions */}
              <div style={{ display: 'flex', gap: 8, marginTop: 18 }}>
                <button
                  type='button'
                  className='btn'
                  disabled={saving}
                  onClick={handleRotate}
                >
                  {tr('console.token.rotate_key', 'rotate key')}
                </button>
                <button
                  type='button'
                  className='btn'
                  onClick={() => navigate('/console/v2/log')}
                >
                  {tr('console.token.view_logs', 'view logs')}
                </button>
                <button
                  type='button'
                  className='btn'
                  onClick={() => navigate('/console/v2/playground')}
                >
                  {tr('console.token.test_playground', 'test in playground')}
                </button>
                <span style={{ flex: 1 }} />
                <button
                  type='button'
                  className='btn'
                  disabled={saving}
                  style={{
                    color: 'var(--hf-err)',
                    borderColor: 'var(--hf-err)',
                  }}
                  onClick={handleRevoke}
                >
                  {tr('console.token.revoke', 'revoke')}
                </button>
              </div>
            </>
          )}
        </div>
      </div>

      {creating && (
        <CreateModal
          tenantSlug={tenantSlug}
          onCreated={handleCreated}
          onClose={() => setCreating(false)}
        />
      )}

      <ConfirmDialog
        visible={confirmIntent === 'revoke'}
        title={tr('撤销令牌 "{{name}}"?', { name: token?.name || '' })}
        consequenceList={[tr('旧密钥将立即失效'), tr('此操作无法撤销')]}
        confirmText={token?.name || ''}
        onConfirm={performRevoke}
        onCancel={() => !saving && setConfirmIntent(null)}
      />

      <ConfirmDialog
        visible={confirmIntent === 'rotate'}
        title={tr('轮换密钥 "{{name}}"?', { name: token?.name || '' })}
        consequenceList={[tr('旧密钥将立即失效')]}
        confirmText={token?.name || ''}
        confirmButtonType='warning'
        onConfirm={performRotate}
        onCancel={() => !saving && setConfirmIntent(null)}
      />

      <ConfirmDialog
        visible={confirmBatchDelete}
        title={tr('删除 {{n}} 个令牌?', { n: marked.size })}
        consequenceList={[tr('选中令牌的密钥将立即失效'), tr('此操作无法撤销')]}
        confirmText={`delete ${marked.size} tokens`}
        onConfirm={performBatchDelete}
        onCancel={() => !saving && setConfirmBatchDelete(false)}
      />
    </HFShell>
  );
};

export default HFToken;
