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
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import NotAvailable from '../../../components/hifi/NotAvailable';
import HfSkeletonRows from '../../../components/hifi/HfSkeletonRows';
import { API, showError, showSuccess } from '../../../helpers';

// Bounded concurrency for the "test all enabled" sweep — never fan out an
// unbounded burst of upstream test calls.
const BATCH_TEST_CONCURRENCY = 4;

// ─── SyncModelsModal ─────────────────────────────────────────────────────────

const SyncModelsModal = ({ tenantSlug, channel, onClose, onApply }) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [diff, setDiff] = useState(null);
  const [selected, setSelected] = useState(new Set());
  const [applying, setApplying] = useState(false);

  useEffect(() => {
    setLoading(true);
    API.get(`/api/v2/${tenantSlug}/channels/${channel.id}/upstream-models`)
      .then((res) => {
        if (res?.data?.success) {
          const d = res.data.data;
          setDiff(d);
          // Pre-select new models by default.
          setSelected(new Set(d.new ?? []));
        } else {
          showError(
            t(
              'console.channel.sync_fetch_failed',
              'Failed to fetch upstream models',
            ),
          );
          onClose();
        }
      })
      .catch(() => {
        showError(
          t(
            'console.channel.sync_fetch_failed',
            'Failed to fetch upstream models',
          ),
        );
        onClose();
      })
      .finally(() => setLoading(false));
  }, [tenantSlug, channel.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggleModel = (m) => {
    setSelected((prev) => {
      const n = new Set(prev);
      if (n.has(m)) n.delete(m);
      else n.add(m);
      return n;
    });
  };

  const handleApply = async () => {
    if (!diff) return;
    setApplying(true);
    try {
      // Merge: start from current list, add selected new, remove missing that are deselected.
      const currentSet = new Set(
        (channel.models || '')
          .split(',')
          .map((m) => m.trim())
          .filter(Boolean),
      );
      // Add selected new models.
      for (const m of selected) {
        currentSet.add(m);
      }
      const newModels = [...currentSet].join(',');
      const res = await API.put(
        `/api/v2/${tenantSlug}/channels/${channel.id}`,
        { models: newModels },
      );
      if (res?.data?.success) {
        showSuccess(
          t('console.channel.toast_synced', 'Upstream models synced'),
        );
        onApply();
      }
    } catch (_) {
    } finally {
      setApplying(false);
    }
  };

  const pillStyle = (active) => ({
    display: 'inline-block',
    fontFamily: 'var(--hf-mono)',
    fontSize: 10,
    padding: '2px 6px',
    borderRadius: 3,
    border: `1px solid ${active ? 'var(--hf-ok)' : 'var(--hf-rule)'}`,
    background: active ? 'rgba(40,200,90,0.1)' : 'var(--hf-sunken)',
    color: active ? 'var(--hf-ok)' : 'var(--hf-ink)',
    cursor: 'pointer',
    userSelect: 'none',
  });

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
      <div
        style={{
          background: 'var(--hf-paper)',
          border: '1px solid var(--hf-rule)',
          borderRadius: 4,
          padding: 28,
          width: 560,
          maxHeight: '80vh',
          overflowY: 'auto',
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div className='strong' style={{ fontSize: 15 }}>
          {t('console.channel.sync_title', 'Sync upstream models')} ·{' '}
          {channel.name}
        </div>

        {loading ? (
          <div className='muted' style={{ fontSize: 12 }}>
            {t('console.channel.sync_loading', 'Fetching upstream models…')}
          </div>
        ) : diff ? (
          <>
            <div>
              <div className='lbl' style={{ marginBottom: 6 }}>
                {t(
                  'console.channel.sync_upstream_count',
                  'upstream models ({{count}} total)',
                  { count: diff.upstream?.length ?? 0 },
                )}
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {(diff.upstream ?? []).map((m) => (
                  <span key={m} style={pillStyle(false)}>
                    {m}
                  </span>
                ))}
              </div>
            </div>

            {diff.new?.length > 0 && (
              <div>
                <div
                  className='lbl'
                  style={{ marginBottom: 6, color: 'var(--hf-ok)' }}
                >
                  {t(
                    'console.channel.sync_new_count',
                    '{{count}} new models — click to toggle',
                    { count: diff.new.length },
                  )}
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                  {diff.new.map((m) => (
                    <span
                      key={m}
                      data-testid={`sync-new-${m}`}
                      style={pillStyle(selected.has(m))}
                      onClick={() => toggleModel(m)}
                    >
                      + {m}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {diff.missing?.length > 0 && (
              <div>
                <div
                  className='lbl'
                  style={{ marginBottom: 6, color: 'var(--hf-warn)' }}
                >
                  {t(
                    'console.channel.sync_removed_count',
                    '{{count}} models removed upstream',
                    { count: diff.missing.length },
                  )}
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                  {diff.missing.map((m) => (
                    <span
                      key={m}
                      style={{
                        ...pillStyle(false),
                        color: 'var(--hf-warn)',
                        borderColor: 'var(--hf-warn)',
                        textDecoration: 'line-through',
                      }}
                    >
                      {m}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {diff.new?.length === 0 && diff.missing?.length === 0 && (
              <div className='muted' style={{ fontSize: 12 }}>
                {t(
                  'console.channel.sync_up_to_date',
                  'Model list is already up to date — nothing to sync.',
                )}
              </div>
            )}
          </>
        ) : null}

        <div
          style={{
            display: 'flex',
            gap: 8,
            justifyContent: 'flex-end',
            marginTop: 4,
          }}
        >
          <button type='button' className='btn ghost' onClick={onClose}>
            {t('console.common.cancel', 'cancel')}
          </button>
          <button
            type='button'
            className='btn primary'
            data-testid='sync-apply-btn'
            disabled={applying || loading || selected.size === 0}
            onClick={handleApply}
          >
            {applying
              ? t('console.channel.sync_applying', 'applying…')
              : t('console.channel.sync_apply', 'apply selected models')}
          </button>
        </div>
      </div>
    </div>
  );
};

/*
 * HiFi 2 — Channel management wired to live backend.
 * Pattern follows Token page (web/src/pages/v2/Token/index.jsx).
 */

// ─── Tenant slug hook ─────────────────────────────────────────────────────────

const useTenantSlug = () => {
  const [slug, setSlug] = useState('default');
  useEffect(() => {
    try {
      const s = localStorage.getItem('tenant_slug');
      if (s) setSlug(s);
    } catch (_) {}
  }, []);
  return slug;
};

// ─── Status helpers ───────────────────────────────────────────────────────────

const channelStatus = (ch) => {
  if (ch.status === 1) return 'ok';
  if (ch.status === 2) return 'disabled';
  return 'error';
};

const statusDot = (st) =>
  st === 'ok' ? 'dot ok' : st === 'disabled' ? 'dot warn' : 'dot err';

const statusTag = (st) =>
  st === 'ok' ? 'tag ok' : st === 'disabled' ? 'tag warn' : 'tag';

// ─── Model mapping parser ─────────────────────────────────────────────────────

// ModelMapping is stored as "from1=to1,from2=to2" or JSON string.
const parseMapping = (raw) => {
  if (!raw) return [];
  try {
    const obj = JSON.parse(raw);
    return Object.entries(obj).map(([from, to]) => [from, to]);
  } catch (_) {}
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const [from, to] = s.split('=').map((x) => x.trim());
      return [from || s, to || s];
    });
};

// ─── Inline field editor ──────────────────────────────────────────────────────

const InlineEdit = ({ value, multiline, onSave, onCancel }) => {
  const [v, setV] = useState(value);
  const ref = useRef(null);

  useEffect(() => {
    ref.current?.focus();
    if (!multiline) ref.current?.select();
  }, [multiline]);

  const commit = () => {
    const trimmed = v.trim();
    if (trimmed !== value.trim()) onSave(trimmed);
    else onCancel();
  };

  const inputStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 11,
    padding: '3px 6px',
    width: '100%',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
    resize: 'vertical',
  };

  if (multiline) {
    return (
      <textarea
        ref={ref}
        rows={3}
        style={inputStyle}
        value={v}
        onChange={(e) => setV(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onCancel();
        }}
        onBlur={commit}
      />
    );
  }
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

// ─── Create / Edit modal ──────────────────────────────────────────────────────

const EMPTY_FORM = {
  name: '',
  key: '',
  type: 1,
  baseURL: '',
  models: '',
  group: 'default',
  weight: 1,
  priority: 0,
  modelMapping: '',
  tag: '',
  remark: '',
};

// ChannelModal handles three intents from two props:
//   existing       → edit (PUT)
//   prefill (clone)→ create a new channel pre-filled from another (POST)
//   neither        → create blank (POST)
// Clone never copies the upstream key (secret, masked server-side) and appends
// " copy" to the name so the duplicate is obvious.
const ChannelModal = ({ tenantSlug, existing, prefill, onDone, onClose }) => {
  const { t } = useTranslation();
  const source = existing || prefill;
  const [form, setForm] = useState(
    source
      ? {
          name: existing
            ? (source.name ?? '')
            : `${source.name ?? 'channel'} copy`,
          key: '',
          type: source.type ?? 1,
          baseURL: source.base_url ?? '',
          models: Array.isArray(source.models)
            ? source.models.join(',')
            : (source.models ?? ''),
          group: source.group ?? 'default',
          weight: source.weight ?? 1,
          priority: source.priority ?? 0,
          modelMapping: source.model_mapping ?? '',
          tag: source.tag ?? '',
          remark: source.remark ?? '',
        }
      : { ...EMPTY_FORM },
  );
  const [saving, setSaving] = useState(false);
  const nameRef = useRef(null);

  useEffect(() => {
    nameRef.current?.focus();
  }, []);

  const set = (field) => (e) =>
    setForm((f) => ({ ...f, [field]: e.target.value }));

  const submit = async (e) => {
    e.preventDefault();
    if (!form.name.trim()) return;
    setSaving(true);
    try {
      const body = {
        name: form.name.trim(),
        type: Number(form.type),
        base_url: form.baseURL.trim(),
        models: form.models.trim(),
        group: form.group.trim() || 'default',
        weight: Number(form.weight) || 1,
        priority: Number(form.priority) || 0,
        model_mapping: form.modelMapping.trim(),
        tag: form.tag.trim(),
        remark: form.remark.trim(),
      };
      if (!existing && form.key.trim()) body.key = form.key.trim();

      let res;
      if (existing) {
        res = await API.put(
          `/api/v2/${tenantSlug}/channels/${existing.id}`,
          body,
        );
      } else {
        res = await API.post(`/api/v2/${tenantSlug}/channels`, body);
      }

      if (res?.data?.success) {
        showSuccess(
          existing
            ? t('console.channel.toast_updated', 'Channel updated')
            : t('console.channel.toast_created', 'Channel created'),
        );
        onDone();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  const inputStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 11,
    padding: '5px 8px',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
    width: '100%',
  };

  const field = (label, key, extra = {}) => (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span className='lbl'>{label}</span>
      <input
        style={inputStyle}
        value={form[key]}
        onChange={set(key)}
        {...extra}
      />
    </label>
  );

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
          width: 480,
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
          maxHeight: '90vh',
          overflowY: 'auto',
        }}
      >
        <div className='strong' style={{ fontSize: 15 }}>
          {existing
            ? `${t('console.channel.modal_edit', 'Edit')} · ${existing.name}`
            : prefill
              ? `${t('console.channel.modal_clone', 'Clone')} · ${prefill.name}`
              : t('console.channel.modal_new', 'New channel')}
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className='lbl'>
            {t('console.channel.field_name', 'name *')}
          </span>
          <input
            ref={nameRef}
            style={inputStyle}
            value={form.name}
            onChange={set('name')}
            placeholder={t('console.channel.ph_name', 'e.g. openai/main')}
            required
          />
        </label>

        {!existing && (
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <span className='lbl'>
              {t('console.channel.field_api_key', 'api key')}
            </span>
            <input
              style={inputStyle}
              value={form.key}
              onChange={set('key')}
              placeholder='sk-...'
            />
          </label>
        )}

        {field(t('console.channel.field_base_url', 'base url'), 'baseURL', {
          placeholder: 'https://api.openai.com/v1',
        })}

        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className='lbl'>
            {t('console.channel.field_type', 'type (int)')}
          </span>
          <input
            style={inputStyle}
            type='number'
            min='1'
            value={form.type}
            onChange={set('type')}
          />
        </label>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className='lbl'>
            {t('console.channel.field_models', 'models (comma-separated)')}
          </span>
          <input
            style={inputStyle}
            value={form.models}
            onChange={set('models')}
            placeholder='gpt-4o,gpt-4o-mini'
          />
        </label>

        {field(t('console.channel.field_group', 'group'), 'group', {
          placeholder: 'default',
        })}

        <div
          style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}
        >
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <span className='lbl'>
              {t('console.channel.field_weight', 'weight')}
            </span>
            <input
              style={inputStyle}
              type='number'
              min='0'
              step='0.1'
              value={form.weight}
              onChange={set('weight')}
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <span className='lbl'>
              {t('console.channel.field_priority', 'priority')}
            </span>
            <input
              style={inputStyle}
              type='number'
              value={form.priority}
              onChange={set('priority')}
            />
          </label>
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className='lbl'>
            {t(
              'console.channel.field_model_mapping',
              'model mapping (from=to, comma-separated or JSON)',
            )}
          </span>
          <input
            style={inputStyle}
            value={form.modelMapping}
            onChange={set('modelMapping')}
            placeholder='gpt-4=gpt-4o,gpt-3.5-turbo=gpt-4o-mini'
          />
        </label>

        {field(t('console.channel.field_tag', 'tag'), 'tag', {
          placeholder: t('console.channel.ph_tag', 'optional tag'),
        })}

        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className='lbl'>
            {t('console.channel.field_remark', 'remark')}
          </span>
          <input
            style={inputStyle}
            value={form.remark}
            onChange={set('remark')}
            placeholder={t('console.channel.ph_remark', 'optional note')}
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
            {t('console.common.cancel', 'cancel')}
          </button>
          <button type='submit' className='btn primary' disabled={saving}>
            {saving
              ? t('console.channel.saving', 'saving…')
              : existing
                ? t('console.channel.save_changes', 'save changes')
                : t('console.channel.create_channel', 'create channel')}
          </button>
        </div>
      </form>
    </div>
  );
};

// ─── Expanded row detail ──────────────────────────────────────────────────────

const ExpandedRow = ({ channel, tenantSlug, onRefresh }) => {
  const { t } = useTranslation();
  const [editField, setEditField] = useState(null);
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const mappings = parseMapping(channel.model_mapping);

  const modelsList = (() => {
    if (!channel.models) return [];
    if (Array.isArray(channel.models)) return channel.models;
    return channel.models
      .split(',')
      .map((m) => m.trim())
      .filter(Boolean);
  })();

  const saveField = async (field, value) => {
    setEditField(null);
    const body = {};
    if (field === 'baseURL') body.base_url = value;
    else if (field === 'group') body.group = value || 'default';
    else if (field === 'weight') body.weight = Number(value) || 1;
    else if (field === 'priority') body.priority = Number(value) || 0;
    else if (field === 'remark') body.remark = value;
    else if (field === 'models') body.models = value;
    else if (field === 'modelMapping') body.model_mapping = value;
    if (Object.keys(body).length === 0) return;
    setSaving(true);
    try {
      const res = await API.put(
        `/api/v2/${tenantSlug}/channels/${channel.id}`,
        body,
      );
      if (res?.data?.success) {
        showSuccess(t('console.channel.toast_saved', 'Saved'));
        onRefresh();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  // Tier 1.3: open the typed-confirmation dialog instead of window.confirm.
  const handleDelete = () => setConfirmDelete(true);

  const performDelete = async () => {
    setSaving(true);
    try {
      const res = await API.delete(
        `/api/v2/${tenantSlug}/channels/${channel.id}`,
      );
      if (res?.data?.success) {
        showSuccess(t('console.channel.toast_deleted', 'Channel deleted'));
        setConfirmDelete(false);
        onRefresh();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  const toggleStatus = async () => {
    const newStatus = channel.status === 1 ? 2 : 1;
    setSaving(true);
    try {
      const res = await API.put(
        `/api/v2/${tenantSlug}/channels/${channel.id}`,
        { status: newStatus },
      );
      if (res?.data?.success) {
        showSuccess(
          newStatus === 1
            ? t('console.channel.toast_enabled', 'Channel enabled')
            : t('console.channel.toast_disabled', 'Channel disabled'),
        );
        onRefresh();
      }
    } catch (_) {
    } finally {
      setSaving(false);
    }
  };

  const row = (label, value, field, multiline) => (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '110px 1fr auto',
        padding: '8px 0',
        borderBottom: '1px dashed var(--hf-rule)',
        fontSize: 11,
        alignItems: 'center',
        gap: 8,
      }}
    >
      <span className='lbl'>{label}</span>
      {editField === field ? (
        <InlineEdit
          value={String(value ?? '')}
          multiline={multiline}
          onSave={(v) => saveField(field, v)}
          onCancel={() => setEditField(null)}
        />
      ) : (
        <span className='mono' style={{ fontSize: 11, wordBreak: 'break-all' }}>
          {value !== '' && value != null ? String(value) : '—'}
        </span>
      )}
      {editField !== field && (
        <button
          type='button'
          className='btn ghost sm'
          disabled={saving}
          onClick={() => setEditField(field)}
        >
          {t('console.common.edit', 'edit')}
        </button>
      )}
    </div>
  );

  return (
    <div
      style={{
        padding: 20,
        display: 'grid',
        gridTemplateColumns: '1.4fr 1fr 1fr',
        gap: 20,
      }}
    >
      {/* Column 1: settings */}
      <div>
        <div className='lbl' style={{ marginBottom: 8 }}>
          {t('console.channel.settings', 'channel settings')}
        </div>
        <div>
          {row(
            t('console.channel.field_base_url', 'base url'),
            channel.base_url,
            'baseURL',
          )}
          {row(
            t('console.channel.field_group', 'group'),
            channel.group,
            'group',
          )}
          {row(
            t('console.channel.field_weight', 'weight'),
            channel.weight,
            'weight',
          )}
          {row(
            t('console.channel.field_priority', 'priority'),
            channel.priority,
            'priority',
          )}
          {row(
            t('console.channel.field_remark', 'remark'),
            channel.remark,
            'remark',
          )}
        </div>
      </div>

      {/* Column 2: models */}
      <div>
        <div className='lbl' style={{ marginBottom: 8 }}>
          {t('console.channel.models_count', 'models ({{count}})', {
            count: modelsList.length,
          })}
        </div>
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 4,
            maxHeight: 180,
            overflowY: 'auto',
          }}
        >
          {modelsList.length === 0 && (
            <span className='muted' style={{ fontSize: 11 }}>
              {t('console.channel.no_models', 'no models configured')}
            </span>
          )}
          {modelsList.map((m, i) => (
            <span key={i} className='pill mono' style={{ fontSize: 10 }}>
              {m}
            </span>
          ))}
        </div>
        <button
          type='button'
          className='btn sm'
          style={{ marginTop: 8 }}
          disabled={saving}
          onClick={() => setEditField('models')}
        >
          {t('console.channel.edit_models', 'edit models')}
        </button>
        {editField === 'models' && (
          <div style={{ marginTop: 6 }}>
            <InlineEdit
              value={modelsList.join(',')}
              onSave={(v) => saveField('models', v)}
              onCancel={() => setEditField(null)}
            />
          </div>
        )}
      </div>

      {/* Column 3: model mapping + actions */}
      <div>
        <div className='lbl' style={{ marginBottom: 8 }}>
          {t('console.channel.mapping', 'model mapping')}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          {mappings.length === 0 && (
            <span className='muted' style={{ fontSize: 11 }}>
              {t('console.channel.mapping_none', 'none')}
            </span>
          )}
          {mappings.map(([from, to], j) => (
            <div
              key={j}
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr 16px 1fr',
                gap: 6,
                alignItems: 'center',
                fontSize: 10,
              }}
            >
              <span className='pill mono'>{from}</span>
              <span className='faint' style={{ textAlign: 'center' }}>
                →
              </span>
              <span className='pill mono'>{to}</span>
            </div>
          ))}
          {editField === 'modelMapping' ? (
            <div style={{ marginTop: 4 }}>
              <InlineEdit
                value={channel.model_mapping ?? ''}
                onSave={(v) => saveField('modelMapping', v)}
                onCancel={() => setEditField(null)}
              />
            </div>
          ) : (
            <button
              type='button'
              className='btn sm'
              style={{ alignSelf: 'flex-start', marginTop: 4 }}
              disabled={saving}
              onClick={() => setEditField('modelMapping')}
            >
              {t('console.channel.edit_mapping', 'edit mapping')}
            </button>
          )}
        </div>

        <div
          style={{ display: 'flex', gap: 6, marginTop: 16, flexWrap: 'wrap' }}
        >
          <button
            type='button'
            className='btn sm'
            disabled={saving}
            onClick={toggleStatus}
          >
            {channel.status === 1
              ? t('console.channel.btn_disable', 'disable')
              : t('console.channel.btn_enable', 'enable')}
          </button>
          {/* Honestly deferred — these New-API features have no v2 backend yet,
              so they are visibly greyed with a reason, never silently absent. */}
          <button
            type='button'
            className='btn sm'
            disabled
            title={t(
              'console.channel.refresh_balance_deferred',
              'upstream balance refresh is provider-specific and unreliable — not wired in v2',
            )}
          >
            {t('console.channel.refresh_balance', 'refresh balance')}
          </button>
          <button
            type='button'
            className='btn sm'
            disabled
            title={t(
              'console.channel.multi_key_deferred',
              'multi-key management needs a backend key-pool model — deferred',
            )}
          >
            {t('console.channel.multi_key', 'multi-key')}
          </button>
          <button
            type='button'
            className='btn sm'
            style={{ color: 'var(--hf-err)', borderColor: 'var(--hf-err)' }}
            disabled={saving}
            onClick={handleDelete}
          >
            {t('console.common.delete', 'delete')}
          </button>
        </div>
      </div>

      <ConfirmDialog
        visible={confirmDelete}
        title={t(
          'console.channel.confirm_delete_title',
          'Delete channel "{{name}}"?',
          { name: channel.name },
        )}
        consequenceList={[
          t(
            'console.channel.confirm_delete_route',
            'Requests will stop routing through this channel',
          ),
          t(
            'console.channel.confirm_irreversible',
            'This action cannot be undone',
          ),
        ]}
        confirmText={channel.name}
        onConfirm={performDelete}
        onCancel={() => !saving && setConfirmDelete(false)}
      />
    </div>
  );
};

// ─── Main page ────────────────────────────────────────────────────────────────

const HFChannel = () => {
  const { t } = useTranslation();
  const tenantSlug = useTenantSlug();

  const [channels, setChannels] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  // Selection & expansion are keyed by channel id (not list index) so they stay
  // correct when the client-side status filter changes the rendered set.
  const [sel, setSel] = useState(new Set());
  const [open, setOpen] = useState(null); // expanded channel id, or null
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState(null); // channel object being edited
  const [cloning, setCloning] = useState(null); // channel object being cloned
  const [syncTarget, setSyncTarget] = useState(null); // channel being synced
  // testState: { [channelId]: 'testing' | { latency_ms, success, error? } }
  const [testState, setTestState] = useState({});
  // batchTesting: { done, total } while a "test all enabled" sweep runs, else null
  const [batchTesting, setBatchTesting] = useState(null);

  // Filters — keyword + group are server-side (ListChannelsV2 supports them);
  // status is client-side (the list endpoint has no status filter).
  const [filterKeyword, setFilterKeyword] = useState('');
  const [filterGroup, setFilterGroup] = useState('');
  const [filterStatus, setFilterStatus] = useState('all'); // all|ok|disabled|error

  const fetchChannels = useCallback(
    async (keyword = '', group = '') => {
      setLoading(true);
      try {
        const params = new URLSearchParams({ page: '1', page_size: '100' });
        if (keyword.trim()) params.set('keyword', keyword.trim());
        if (group.trim()) params.set('group', group.trim());
        const res = await API.get(
          `/api/v2/${tenantSlug}/channels?${params.toString()}`,
        );
        if (res?.data?.success) {
          const d = res.data.data;
          setChannels(d.channels ?? []);
          setTotal(d.total ?? d.channels?.length ?? 0);
          setSel(new Set());
          setOpen(null);
        }
      } catch (_) {
      } finally {
        setLoading(false);
      }
    },
    [tenantSlug],
  );

  // Re-fetch preserving the active keyword/group filters — used by every
  // post-mutation refresh so an edit doesn't silently drop the filter.
  const applyChannelFilters = useCallback(() => {
    fetchChannels(filterKeyword, filterGroup);
  }, [fetchChannels, filterKeyword, filterGroup]);

  useEffect(() => {
    if (tenantSlug) fetchChannels();
  }, [fetchChannels, tenantSlug]);

  // Status filter is client-side; clear stale selection/expansion when it
  // changes so id-based selection never points at a now-hidden row.
  const visibleChannels = useMemo(() => {
    if (filterStatus === 'all') return channels;
    return channels.filter((c) => channelStatus(c) === filterStatus);
  }, [channels, filterStatus]);

  useEffect(() => {
    setSel(new Set());
    setOpen(null);
  }, [filterStatus]);

  const toggle = (id) => {
    setSel((prev) => {
      const n = new Set(prev);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  };

  const handleModalDone = async () => {
    setCreating(false);
    setEditing(null);
    setCloning(null);
    await applyChannelFilters();
  };

  const handleTestChannel = async (e, ch) => {
    e.stopPropagation();
    const id = ch.id;
    setTestState((prev) => ({ ...prev, [id]: 'testing' }));
    try {
      const res = await API.post(
        `/api/v2/${tenantSlug}/channels/${id}/test`,
        {},
      );
      const data = res?.data ?? {};
      setTestState((prev) => ({ ...prev, [id]: data }));
      if (data.success) {
        showSuccess(
          t('console.channel.toast_latency', 'Channel latency {{ms}}ms', {
            ms: data.latency_ms ?? 0,
          }),
        );
      } else {
        showError(
          t('console.channel.toast_test_failed', 'Channel test failed') +
            (data.error ? `: ${data.error}` : ''),
        );
      }
    } catch (err) {
      setTestState((prev) => ({
        ...prev,
        [id]: { success: false, error: String(err) },
      }));
      showError(t('console.channel.toast_test_failed', 'Channel test failed'));
    }
  };

  // Test every enabled channel with bounded concurrency. Each channel keeps its
  // own per-row result (reusing testState); a shared cursor caps the in-flight
  // count at BATCH_TEST_CONCURRENCY. Idempotent — re-running just re-tests.
  const runBatchTest = async () => {
    if (batchTesting) return;
    const targets = channels.filter((c) => c.status === 1);
    if (targets.length === 0) {
      showError(
        t('console.channel.no_enabled_channels', 'No enabled channels to test'),
      );
      return;
    }
    setBatchTesting({ done: 0, total: targets.length });
    let cursor = 0;
    let okCnt = 0;
    const worker = async () => {
      // cursor++ is synchronous between awaits, so each channel is claimed once.
      while (cursor < targets.length) {
        const ch = targets[cursor++];
        setTestState((prev) => ({ ...prev, [ch.id]: 'testing' }));
        try {
          const res = await API.post(
            `/api/v2/${tenantSlug}/channels/${ch.id}/test`,
            {},
          );
          const data = res?.data ?? {};
          setTestState((prev) => ({ ...prev, [ch.id]: data }));
          if (data.success) okCnt += 1;
        } catch (err) {
          setTestState((prev) => ({
            ...prev,
            [ch.id]: { success: false, error: String(err) },
          }));
        } finally {
          setBatchTesting((b) => (b ? { ...b, done: b.done + 1 } : b));
        }
      }
    };
    await Promise.all(
      Array.from(
        { length: Math.min(BATCH_TEST_CONCURRENCY, targets.length) },
        () => worker(),
      ),
    );
    setBatchTesting(null);
    showSuccess(
      t(
        'console.channel.toast_batch_done',
        'Batch test complete: {{ok}}/{{total}} passed',
        { ok: okCnt, total: targets.length },
      ),
    );
  };

  // Derived summary counts (always over the full set, not the filtered view).
  const okCount = channels.filter((c) => channelStatus(c) === 'ok').length;
  const disabledCount = channels.filter(
    (c) => channelStatus(c) === 'disabled',
  ).length;
  const errorCount = channels.filter(
    (c) => channelStatus(c) === 'error',
  ).length;

  // Batch enable/disable — selection holds channel ids.
  const batchSetStatus = async (status) => {
    const targets = [...sel]
      .map((id) => channels.find((c) => c.id === id))
      .filter(Boolean);
    if (targets.length === 0) return;
    try {
      await Promise.all(
        targets.map((ch) =>
          API.put(`/api/v2/${tenantSlug}/channels/${ch.id}`, { status }),
        ),
      );
      showSuccess(
        t('console.channel.toast_batch_updated', '{{count}} channels updated', {
          count: targets.length,
        }),
      );
      await applyChannelFilters();
    } catch (_) {}
  };

  return (
    <HFShell
      active='channels'
      crumbs={[
        t('console.nav.section_platform_admin', 'platform · admin'),
        t('console.channel.crumb', 'channels'),
      ]}
      actions={
        <>
          <button
            type='button'
            className='btn'
            data-testid='batch-test-all-btn'
            disabled={!!batchTesting || loading}
            onClick={runBatchTest}
            title={t(
              'console.channel.test_all_title',
              'test every enabled channel (max 4 in parallel)',
            )}
          >
            {batchTesting
              ? t(
                  'console.channel.testing_progress',
                  'testing {{done}}/{{total}}',
                  { done: batchTesting.done, total: batchTesting.total },
                )
              : t('console.channel.test_all', 'test all enabled')}
          </button>
          <button
            type='button'
            className='btn primary'
            onClick={() => setCreating(true)}
          >
            {t('console.channel.new_channel', '+ new channel')}
          </button>
        </>
      }
    >
      {/* Page header */}
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {t('console.channel.crumb', 'channels')}
          </div>
          <h1>
            {loading
              ? '…'
              : t('console.channel.count', '{{count}} upstream channels', {
                  count: total,
                })}
          </h1>
          <div className='sub'>
            {t('console.channel.sub', 'channel management · live data')}
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 28 }}>
          {[
            [
              t('console.channel.stat_healthy', 'healthy'),
              loading ? '…' : String(okCount),
              'var(--hf-ok)',
            ],
            [
              t('console.channel.stat_disabled', 'disabled'),
              loading ? '…' : String(disabledCount),
              'var(--hf-warn)',
            ],
            [
              t('console.channel.stat_error', 'error'),
              loading ? '…' : String(errorCount),
              'var(--hf-err)',
            ],
          ].map(([l, v, c], i) => (
            <div key={i}>
              <div className='lbl'>{l}</div>
              <div
                className='display'
                style={{ fontSize: 26, color: c, marginTop: 2 }}
              >
                {v}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Filter bar — keyword + group are server-side; status is client-side */}
      <div
        style={{
          padding: '10px 28px',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
          flexWrap: 'wrap',
        }}
      >
        <input
          data-testid='channel-filter-keyword'
          style={{
            fontFamily: 'var(--hf-mono)',
            fontSize: 11,
            padding: '4px 8px',
            border: '1px solid var(--hf-rule)',
            background: 'var(--hf-sunken)',
            color: 'var(--hf-ink)',
            borderRadius: 2,
            outline: 'none',
            width: 180,
          }}
          placeholder={t('console.channel.ph_search', 'search name / key…')}
          value={filterKeyword}
          onChange={(e) => setFilterKeyword(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && applyChannelFilters()}
        />
        <input
          data-testid='channel-filter-group'
          style={{
            fontFamily: 'var(--hf-mono)',
            fontSize: 11,
            padding: '4px 8px',
            border: '1px solid var(--hf-rule)',
            background: 'var(--hf-sunken)',
            color: 'var(--hf-ink)',
            borderRadius: 2,
            outline: 'none',
            width: 130,
          }}
          placeholder={t('console.channel.ph_group', 'group…')}
          value={filterGroup}
          onChange={(e) => setFilterGroup(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && applyChannelFilters()}
        />
        <select
          data-testid='channel-filter-status'
          style={{
            fontFamily: 'var(--hf-mono)',
            fontSize: 11,
            padding: '4px 8px',
            border: '1px solid var(--hf-rule)',
            background: 'var(--hf-sunken)',
            color: 'var(--hf-ink)',
            borderRadius: 2,
            outline: 'none',
          }}
          value={filterStatus}
          onChange={(e) => setFilterStatus(e.target.value)}
        >
          <option value='all'>
            {t('console.channel.filter_all', 'all status')}
          </option>
          <option value='ok'>
            {t('console.channel.filter_enabled', 'enabled')}
          </option>
          <option value='disabled'>
            {t('console.channel.filter_disabled', 'disabled')}
          </option>
          <option value='error'>
            {t('console.channel.filter_error', 'error')}
          </option>
        </select>
        <button
          type='button'
          className='btn primary'
          onClick={applyChannelFilters}
        >
          {t('console.common.search', 'search')}
        </button>
        <button
          type='button'
          className='btn ghost'
          onClick={() => {
            setFilterKeyword('');
            setFilterGroup('');
            setFilterStatus('all');
            fetchChannels('', '');
          }}
        >
          {t('console.channel.clear', 'clear')}
        </button>
      </div>

      {/* Batch action bar */}
      <div
        style={{
          padding: '10px 28px',
          background: sel.size ? 'var(--hf-accent)' : 'var(--hf-paper)',
          color: sel.size ? '#fff' : 'var(--hf-ink-3)',
          borderBottom: '1px solid var(--hf-rule)',
          fontFamily: 'var(--hf-mono)',
          fontSize: 11,
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          transition: 'background 0.15s',
        }}
      >
        {sel.size ? (
          <>
            <span>
              <b>{sel.size}</b> {t('console.channel.selected', 'selected')}
            </span>
            <span style={{ opacity: 0.5 }}>·</span>
            <button
              type='button'
              className='btn'
              style={{
                background: 'rgba(255,255,255,0.15)',
                borderColor: 'rgba(255,255,255,0.3)',
                color: '#fff',
              }}
              onClick={() => batchSetStatus(1)}
            >
              {t('console.channel.btn_enable', 'enable')}
            </button>
            <button
              type='button'
              className='btn'
              style={{
                background: 'rgba(255,255,255,0.15)',
                borderColor: 'rgba(255,255,255,0.3)',
                color: '#fff',
              }}
              onClick={() => batchSetStatus(2)}
            >
              {t('console.channel.btn_disable', 'disable')}
            </button>
            <span style={{ flex: 1 }} />
            <button
              type='button'
              className='btn ghost'
              style={{ color: '#fff' }}
              onClick={() => setSel(new Set())}
            >
              {t('console.channel.clear', 'clear')}
            </button>
          </>
        ) : (
          <span>
            {t(
              'console.channel.tip',
              'tip · select rows for batch operations · or click any row to expand',
            )}
          </span>
        )}
      </div>

      {/* Table */}
      {loading ? (
        <div style={{ padding: '14px 28px' }}>
          <HfSkeletonRows rows={6} />
        </div>
      ) : visibleChannels.length === 0 ? (
        <div className='muted' style={{ padding: '24px 28px', fontSize: 12 }}>
          {channels.length === 0
            ? t(
                'console.channel.empty',
                'No channels yet. Add one to get started.',
              )
            : t(
                'console.channel.empty_filtered',
                'No channels match the current filters.',
              )}
        </div>
      ) : (
        <div className='hf-table-scroll'>
          <table className='t'>
            <thead>
              <tr>
                <th style={{ width: 32 }}></th>
                <th>{t('console.channel.th_channel', 'channel')}</th>
                <th>{t('console.channel.th_type', 'type')}</th>
                <th>{t('console.channel.th_models', 'models')}</th>
                <th>{t('console.channel.th_qps', 'qps · 1h trend')}</th>
                <th>{t('console.channel.th_latency', 'p50 / p95')}</th>
                <th>{t('console.channel.th_success', 'success')}</th>
                <th>{t('console.channel.th_cost', 'cost · 1h')}</th>
                <th>{t('console.channel.th_status', 'status')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {visibleChannels.map((ch, i) => {
                const st = channelStatus(ch);
                const isOpen = open === ch.id;
                // Row-expand cells below are <td onClick> with no keyboard
                // path (fixPlan #5) — this shared handler gives every one of
                // them Enter/Space parity with the click, without changing
                // what the click itself does.
                const handleExpandKeyDown = (e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    setOpen(isOpen ? null : ch.id);
                  }
                };

                const modelsList = (() => {
                  if (!ch.models) return [];
                  if (Array.isArray(ch.models)) return ch.models;
                  return ch.models
                    .split(',')
                    .map((m) => m.trim())
                    .filter(Boolean);
                })();

                return (
                  <Fragment key={ch.id ?? i}>
                    <tr
                      style={{
                        background: sel.has(ch.id)
                          ? 'rgba(255,93,31,0.08)'
                          : undefined,
                        borderLeft: sel.has(ch.id)
                          ? '2px solid var(--hf-accent)'
                          : '2px solid transparent',
                        cursor: 'pointer',
                      }}
                    >
                      {/* Checkbox — native input carries its own keyboard
                        support (Tab + Space), no role/tabIndex needed. */}
                      <td>
                        <input
                          type='checkbox'
                          checked={sel.has(ch.id)}
                          onChange={() => toggle(ch.id)}
                          aria-label={t(
                            'console.channel.select_channel',
                            'select channel',
                          )}
                        />
                      </td>

                      {/* Name + group */}
                      <td
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        <div
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 10,
                          }}
                        >
                          <span className={statusDot(st)} />
                          <div>
                            <div className='strong'>{ch.name}</div>
                            <div className='faint mono' style={{ fontSize: 9 }}>
                              {ch.group || 'default'}
                              {ch.tag ? ` · ${ch.tag}` : ''}
                            </div>
                          </div>
                        </div>
                      </td>

                      {/* Type */}
                      <td
                        className='mono muted'
                        style={{ fontSize: 11 }}
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        {ch.type ?? '—'}
                      </td>

                      {/* Model count */}
                      <td
                        className='mono'
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        {modelsList.length > 0 ? modelsList.length : '—'}
                      </td>

                      {/* QPS — no metrics backend (no per-channel aggregation) */}
                      <td
                        className='muted'
                        style={{ fontSize: 11 }}
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        <NotAvailable
                          reason={t(
                            'console.channel.na_qps',
                            'no metrics backend: per-channel QPS is not aggregated',
                          )}
                        />
                      </td>

                      {/* Latency p50/p95 — no metrics backend */}
                      <td
                        className='muted'
                        style={{ fontSize: 11 }}
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        <NotAvailable
                          reason={t(
                            'console.channel.na_latency',
                            'no metrics backend: per-channel latency percentiles are not aggregated',
                          )}
                        />
                      </td>

                      {/* Success rate */}
                      <td
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        {ch.success_rate != null && ch.success_rate > 0 ? (
                          <div
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 8,
                            }}
                          >
                            <div
                              style={{
                                width: 60,
                                height: 4,
                                background: 'var(--hf-sunken)',
                              }}
                            >
                              <div
                                style={{
                                  width: `${Math.min(ch.success_rate, 100)}%`,
                                  height: '100%',
                                  background:
                                    ch.success_rate >= 99
                                      ? 'var(--hf-ok)'
                                      : ch.success_rate >= 95
                                        ? 'var(--hf-warn)'
                                        : 'var(--hf-err)',
                                }}
                              />
                            </div>
                            <span
                              className='mono'
                              style={{
                                fontSize: 11,
                                color:
                                  ch.success_rate >= 99
                                    ? 'var(--hf-ok)'
                                    : ch.success_rate >= 95
                                      ? 'var(--hf-warn)'
                                      : 'var(--hf-err)',
                              }}
                            >
                              {Number(ch.success_rate).toFixed(1)}%
                            </span>
                          </div>
                        ) : (
                          <span className='muted' style={{ fontSize: 11 }}>
                            —
                          </span>
                        )}
                      </td>

                      {/* Cost · 1h — no metrics backend */}
                      <td
                        className='muted'
                        style={{ fontSize: 11 }}
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        <NotAvailable
                          reason={t(
                            'console.channel.na_cost',
                            'no metrics backend: per-channel hourly cost is not aggregated',
                          )}
                        />
                      </td>

                      {/* Status badge */}
                      <td
                        onClick={() => setOpen(isOpen ? null : ch.id)}
                        onKeyDown={handleExpandKeyDown}
                        role='button'
                        tabIndex={0}
                        aria-expanded={isOpen}
                      >
                        <span className={statusTag(st)}>
                          {t(`console.channel.status_${st}`, st)}
                        </span>
                      </td>

                      {/* Row actions: test + expand */}
                      <td style={{ whiteSpace: 'nowrap' }}>
                        {(() => {
                          const ts = testState[ch.id];
                          const isTesting = ts === 'testing';
                          const testLabel = isTesting
                            ? t('console.channel.testing', 'testing…')
                            : t('console.channel.test_channel', 'test channel');
                          const testColor =
                            ts && ts !== 'testing'
                              ? ts.success
                                ? 'var(--hf-ok)'
                                : 'var(--hf-err)'
                              : undefined;
                          return (
                            <button
                              type='button'
                              className='btn ghost sm'
                              data-testid={`test-btn-${ch.id}`}
                              disabled={isTesting}
                              style={
                                testColor
                                  ? { color: testColor, borderColor: testColor }
                                  : undefined
                              }
                              onClick={(e) => handleTestChannel(e, ch)}
                            >
                              {testLabel}
                            </button>
                          );
                        })()}
                        <button
                          type='button'
                          className='btn ghost'
                          style={{ marginLeft: 4 }}
                          onClick={() => setOpen(isOpen ? null : ch.id)}
                          aria-label={
                            isOpen
                              ? t('console.channel.collapse_row', 'collapse')
                              : t('console.channel.expand_row', 'expand')
                          }
                          aria-expanded={isOpen}
                        >
                          {isOpen ? '▾' : '▸'}
                        </button>
                      </td>
                    </tr>

                    {/* Expanded detail row */}
                    {isOpen && (
                      <tr>
                        <td
                          colSpan={10}
                          style={{
                            padding: 0,
                            background: 'var(--hf-paper)',
                            borderLeft: '2px solid var(--hf-accent)',
                          }}
                        >
                          <ExpandedRow
                            channel={ch}
                            tenantSlug={tenantSlug}
                            onRefresh={applyChannelFilters}
                          />
                          <div
                            style={{
                              padding: '0 20px 14px',
                              display: 'flex',
                              gap: 8,
                            }}
                          >
                            <button
                              type='button'
                              className='btn sm'
                              onClick={() => setEditing(ch)}
                            >
                              {t('console.channel.edit_all', 'edit all fields')}
                            </button>
                            <button
                              type='button'
                              className='btn sm'
                              data-testid={`clone-btn-${ch.id}`}
                              onClick={() => setCloning(ch)}
                              title={t(
                                'console.channel.clone_title',
                                'create a new channel pre-filled from this one (key not copied)',
                              )}
                            >
                              {t(
                                'console.channel.clone_channel',
                                'clone channel',
                              )}
                            </button>
                            <button
                              type='button'
                              className='btn sm'
                              data-testid={`sync-btn-${ch.id}`}
                              onClick={() => setSyncTarget(ch)}
                            >
                              {t(
                                'console.channel.sync_title',
                                'Sync upstream models',
                              )}
                            </button>
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Create modal */}
      {creating && (
        <ChannelModal
          tenantSlug={tenantSlug}
          existing={null}
          onDone={handleModalDone}
          onClose={() => setCreating(false)}
        />
      )}

      {/* Edit modal */}
      {editing && (
        <ChannelModal
          tenantSlug={tenantSlug}
          existing={editing}
          onDone={handleModalDone}
          onClose={() => setEditing(null)}
        />
      )}

      {/* Clone modal — create pre-filled from an existing channel */}
      {cloning && (
        <ChannelModal
          tenantSlug={tenantSlug}
          prefill={cloning}
          onDone={handleModalDone}
          onClose={() => setCloning(null)}
        />
      )}

      {/* Sync upstream models modal */}
      {syncTarget && (
        <SyncModelsModal
          tenantSlug={tenantSlug}
          channel={syncTarget}
          onClose={() => setSyncTarget(null)}
          onApply={async () => {
            setSyncTarget(null);
            await applyChannelFilters();
          }}
        />
      )}
    </HFShell>
  );
};

export default HFChannel;
