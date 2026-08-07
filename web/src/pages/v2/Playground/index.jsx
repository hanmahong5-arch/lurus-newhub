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
import HFShell from '../../../components/hifi/HFShell';
import { API, showError, showSuccess } from '../../../helpers';
import { useFormDraft } from '../../../hooks/common/useFormDraft';

// HiFi 5 — Playground multi-model compare. Wired to
// POST /api/v2/:tenant_slug/playground/run (2026-05-19).
// Wave 3 Phase 1 — save/blank/swap/share wired (2026-05-20).

const DEFAULT_MODELS = ['gpt-4o', 'claude-3.5-sonnet', 'gemini-1.5-pro'];
const VENDOR_FOR = {
  'gpt-4o': 'OpenAI',
  'claude-3.5-sonnet': 'Anthropic',
  'gemini-1.5-pro': 'Google',
};

const DEFAULT_FORM = {
  system: 'You are a helpful, concise assistant.',
  user: 'What is the capital of Australia? Briefly.',
  temperature: 0.7,
  top_p: 1.0,
  max_tokens: 1024,
  models: DEFAULT_MODELS,
};

// readURLParams extracts ?prompt=&models=&params= from the current URL
// for URL-self-contained share links. Returns partial form fields.
function readURLParams() {
  try {
    const params = new URLSearchParams(window.location.search);
    const out = {};
    if (params.has('prompt')) out.user = params.get('prompt');
    if (params.has('models')) {
      try {
        const m = JSON.parse(params.get('models'));
        if (Array.isArray(m) && m.length > 0) out.models = m;
      } catch (_) {}
    }
    if (params.has('params')) {
      try {
        const p = JSON.parse(params.get('params'));
        if (p && typeof p === 'object') {
          if (typeof p.temperature === 'number')
            out.temperature = p.temperature;
          if (typeof p.top_p === 'number') out.top_p = p.top_p;
          if (typeof p.max_tokens === 'number') out.max_tokens = p.max_tokens;
        }
      } catch (_) {}
    }
    return out;
  } catch (_) {
    return {};
  }
}

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

// Sort columns by latency so the fastest is leftmost — gives the user an
// at-a-glance perf comparison without an extra UI control. Stable: keeps
// original column index as tiebreaker so the layout doesn't reshuffle
// between identical runs. `label` doubles as the i18n key suffix
// (console.playground.verdict_<label>).
const verdictFor = (item, allItems) => {
  if (item.error_code) return { label: 'error', color: 'var(--hf-err)' };
  const sorted = allItems
    .filter((x) => !x.error_code)
    .slice()
    .sort((a, b) => a.latency_ms - b.latency_ms);
  if (sorted.length && item === sorted[0])
    return { label: 'fastest', color: 'var(--hf-accent)' };
  if (sorted.length && item === sorted[sorted.length - 1])
    return { label: 'slowest', color: 'var(--hf-info)' };
  return { label: '', color: 'var(--hf-ink-2)' };
};

const HFPlayground = () => {
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` per the v2 console convention.
  const { t: tr } = useTranslation();

  // Form state — persisted via useFormDraft so a tab close mid-edit doesn't
  // lose the user's prompt. Key NOT scoped per-tenant (playground is a
  // user-level tool; same prompt likely reused across tenants).
  const [form, setForm, clearDraft, , /* isDirty */ restoredFromDraft] =
    useFormDraft('playground-form', DEFAULT_FORM);
  const [running, setRunning] = useState(false);
  const [items, setItems] = useState(null); // null = never run; [] = ran with errors

  // Apply URL prefill on first mount — takes precedence over the draft only
  // when share params are present. Preserves draft for fields not in URL.
  const urlPrefillApplied = useRef(false);
  useEffect(() => {
    if (urlPrefillApplied.current) return;
    urlPrefillApplied.current = true;
    const prefill = readURLParams();
    if (Object.keys(prefill).length > 0) {
      setForm((prev) => ({ ...prev, ...prefill }));
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Preset state ──
  const [presets, setPresets] = useState(null); // null = not yet loaded
  const [blankOpen, setBlankOpen] = useState(false);
  const [swapOpen, setSwapOpen] = useState(null); // colIdx | null
  const [availableModels, setAvailableModels] = useState([]);
  const blankRef = useRef(null);
  const swapRef = useRef(null);

  // Close dropdowns on outside click.
  useEffect(() => {
    const handler = (e) => {
      if (blankRef.current && !blankRef.current.contains(e.target)) {
        setBlankOpen(false);
      }
      if (swapRef.current && !swapRef.current.contains(e.target)) {
        setSwapOpen(null);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const update = (k) => (v) => setForm((prev) => ({ ...prev, [k]: v }));

  // ── Fetch presets (lazy — only when blank▾ is opened) ──
  const fetchPresets = useCallback(async () => {
    if (presets !== null) return; // already loaded
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/playground/presets`);
      if (res?.data?.success) {
        setPresets(res.data.data || []);
      } else {
        setPresets([]);
      }
    } catch (_) {
      setPresets([]);
    }
  }, [presets, tenantSlug]);

  // ── Fetch available models (lazy — only when swap▾ is opened) ──
  const fetchModels = useCallback(async () => {
    if (availableModels.length > 0) return;
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/models`);
      if (res?.data?.success) {
        const names = (res.data.data || [])
          .map((m) => m.model_name || m.name)
          .filter(Boolean);
        setAvailableModels(names);
      }
    } catch (_) {}
  }, [availableModels.length, tenantSlug]);

  const runAll = useCallback(async () => {
    if (!form.user.trim()) {
      showError(
        tr(
          'console.playground.err_empty_prompt',
          'User prompt cannot be empty',
        ),
      );
      return;
    }
    setRunning(true);
    setItems(null);
    try {
      const res = await API.post(`/api/v2/${tenantSlug}/playground/run`, {
        system: form.system,
        user: form.user,
        models: form.models,
        params: {
          temperature: Number(form.temperature),
          top_p: Number(form.top_p),
          max_tokens: Math.max(1, parseInt(form.max_tokens, 10) || 1024),
        },
      });
      if (res?.data?.success) {
        setItems(res.data.data.items);
      } else {
        showError(
          res?.data?.message ||
            tr('console.playground.run_failed', 'Run failed'),
        );
      }
    } catch (err) {
      const msg =
        err?.response?.data?.message ||
        err?.message ||
        tr('console.playground.run_failed', 'Run failed');
      showError(msg);
    } finally {
      setRunning(false);
    }
  }, [form, tenantSlug, tr]);

  // ⌘/Ctrl + Enter triggers run from any focus position inside the page.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !running) {
        e.preventDefault();
        runAll();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [runAll, running]);

  // ── save handler ──
  const handleSave = useCallback(async () => {
    const name = window.prompt(
      tr('console.playground.save_preset_prompt', 'Preset name'),
      '',
    );
    if (!name || !name.trim()) return;
    try {
      const res = await API.post(`/api/v2/${tenantSlug}/playground/presets`, {
        name: name.trim(),
        prompt: form.user,
        models: JSON.stringify(form.models),
        params: JSON.stringify({
          temperature: Number(form.temperature),
          top_p: Number(form.top_p),
          max_tokens: Math.max(1, parseInt(form.max_tokens, 10) || 1024),
        }),
      });
      if (res?.data?.success) {
        showSuccess(tr('console.playground.preset_saved', 'Preset saved'));
        setPresets(null); // invalidate cached list so next open re-fetches
      } else {
        showError(
          res?.data?.message ||
            tr('console.playground.save_failed', 'Save failed'),
        );
      }
    } catch (err) {
      showError(
        err?.response?.data?.message ||
          err?.message ||
          tr('console.playground.save_failed', 'Save failed'),
      );
    }
  }, [form, tenantSlug, tr]);

  // ── blank▾ toggle handler ──
  const handleBlankToggle = useCallback(async () => {
    if (!blankOpen) await fetchPresets();
    setBlankOpen((o) => !o);
    setSwapOpen(null);
  }, [blankOpen, fetchPresets]);

  const loadPreset = useCallback(
    (preset) => {
      let models = form.models;
      let temperature = form.temperature;
      let top_p = form.top_p;
      let max_tokens = form.max_tokens;
      try {
        const m = JSON.parse(preset.models);
        if (Array.isArray(m) && m.length > 0) models = m;
      } catch (_) {}
      try {
        const p = JSON.parse(preset.params);
        if (typeof p.temperature === 'number') temperature = p.temperature;
        if (typeof p.top_p === 'number') top_p = p.top_p;
        if (typeof p.max_tokens === 'number') max_tokens = p.max_tokens;
      } catch (_) {}
      setForm((prev) => ({
        ...prev,
        user: preset.prompt || '',
        models,
        temperature,
        top_p,
        max_tokens,
      }));
      setBlankOpen(false);
    },
    [form, setForm],
  );

  const loadBlank = useCallback(() => {
    setForm(DEFAULT_FORM);
    setBlankOpen(false);
  }, [setForm]);

  // ── share handler — URL self-contained, no backend needed ──
  const handleShare = useCallback(() => {
    try {
      const params = new URLSearchParams();
      params.set('prompt', form.user);
      params.set('models', JSON.stringify(form.models));
      params.set(
        'params',
        JSON.stringify({
          temperature: Number(form.temperature),
          top_p: Number(form.top_p),
          max_tokens: Math.max(1, parseInt(form.max_tokens, 10) || 1024),
        }),
      );
      const url =
        window.location.origin +
        window.location.pathname +
        '?' +
        params.toString();
      navigator.clipboard?.writeText(url);
      showSuccess(tr('console.playground.share_copied', 'Share link copied'));
    } catch (_) {
      showError(
        tr(
          'console.playground.share_copy_failed',
          'Copy failed — copy the URL from the address bar manually',
        ),
      );
    }
  }, [form, tr]);

  // ── swap▾ toggle handler ──
  const handleSwapToggle = useCallback(
    async (colIdx) => {
      if (swapOpen === colIdx) {
        setSwapOpen(null);
        return;
      }
      await fetchModels();
      setSwapOpen(colIdx);
      setBlankOpen(false);
    },
    [swapOpen, fetchModels],
  );

  const toggleModel = useCallback(
    (modelName) => {
      setForm((prev) => {
        const has = prev.models.includes(modelName);
        const next = has
          ? prev.models.filter((m) => m !== modelName)
          : [...prev.models, modelName];
        // Require at least 1 model active.
        return { ...prev, models: next.length > 0 ? next : prev.models };
      });
    },
    [setForm],
  );

  const displayCols = form.models.map((m, i) => {
    const found = items?.[i];
    return {
      idx: i,
      model: m,
      vendor: VENDOR_FOR[m] || '—',
      result: found || null,
    };
  });

  return (
    <HFShell
      active='playground'
      crumbs={[
        tr('console.nav.section_workspace', 'workspace'),
        tr('console.playground.crumb', 'playground'),
      ]}
      actions={
        <>
          <span className='lbl'>
            {tr('console.playground.preset_label', 'preset:')}
          </span>

          {/* blank▾ — dropdown listing user presets + blank/clear option */}
          <div
            ref={blankRef}
            style={{ position: 'relative', display: 'inline-block' }}
          >
            <button
              type='button'
              className='btn'
              onClick={handleBlankToggle}
              data-testid='playground-blank-btn'
            >
              {tr('console.playground.blank_btn', 'blank ▾')}
            </button>
            {blankOpen && (
              <div
                data-testid='playground-blank-dropdown'
                style={{
                  position: 'absolute',
                  top: '100%',
                  left: 0,
                  zIndex: 100,
                  background: 'var(--hf-paper)',
                  border: '1px solid var(--hf-rule)',
                  borderRadius: 4,
                  minWidth: 180,
                  boxShadow: '0 4px 12px rgba(0,0,0,0.12)',
                  padding: '4px 0',
                }}
              >
                <button
                  type='button'
                  className='btn ghost sm'
                  data-testid='playground-blank-clear'
                  onClick={loadBlank}
                  style={{
                    display: 'block',
                    width: '100%',
                    textAlign: 'left',
                    padding: '6px 12px',
                  }}
                >
                  {tr('console.playground.blank_clear', 'blank (clear)')}
                </button>
                {presets && presets.length > 0 && (
                  <div
                    style={{
                      borderTop: '1px solid var(--hf-rule)',
                      marginTop: 4,
                      paddingTop: 4,
                    }}
                  >
                    {presets.map((p) => (
                      <button
                        key={p.id}
                        type='button'
                        className='btn ghost sm'
                        data-testid={`playground-preset-item-${p.id}`}
                        onClick={() => loadPreset(p)}
                        style={{
                          display: 'block',
                          width: '100%',
                          textAlign: 'left',
                          padding: '6px 12px',
                        }}
                      >
                        {p.name}
                      </button>
                    ))}
                  </div>
                )}
                {presets && presets.length === 0 && (
                  <div
                    style={{
                      padding: '6px 12px',
                      color: 'var(--hf-ink-2)',
                      fontSize: 11,
                    }}
                  >
                    {tr('console.playground.no_presets', 'no presets yet')}
                  </div>
                )}
              </div>
            )}
          </div>

          {/* save — prompts for name, POSTs to presets API */}
          <button
            type='button'
            className='btn'
            onClick={handleSave}
            data-testid='playground-save-btn'
          >
            {tr('console.common.save', 'save')}
          </button>

          {/* share↗ — copies URL with current form state as query params */}
          <button
            type='button'
            className='btn'
            onClick={handleShare}
            data-testid='playground-share-btn'
          >
            {tr('console.playground.share', 'share ↗')}
          </button>
        </>
      }
    >
      <div
        style={{
          display: 'grid',
          gridTemplateRows: 'auto auto 1fr auto',
          height: '100%',
        }}
      >
        {/* ── Header + prompt editor ── */}
        <div
          style={{
            padding: '20px 28px',
            background: 'var(--hf-paper)',
            borderBottom: '1px solid var(--hf-rule)',
          }}
        >
          <div className='lbl' style={{ marginBottom: 4 }}>
            {tr('console.playground.compare', 'compare')}
          </div>
          <h1 className='display' style={{ fontSize: 28, margin: 0 }}>
            {tr(
              'console.playground.headline',
              '{{count}} models, one prompt, side by side',
              { count: form.models.length },
            )}
          </h1>

          {restoredFromDraft && (
            <div
              style={{
                marginTop: 10,
                fontSize: 11,
                padding: '4px 10px',
                borderLeft: '2px solid var(--hf-warn)',
                background: 'var(--hf-warn-bg)',
                color: 'var(--hf-ink-2)',
              }}
              data-testid='playground-restored-banner'
            >
              {tr(
                'console.playground.restored_draft',
                'Restored from saved draft.',
              )}{' '}
              <button
                type='button'
                className='btn ghost xs'
                onClick={clearDraft}
              >
                {tr('console.playground.discard', 'Discard')}
              </button>
            </div>
          )}

          <div
            style={{
              marginTop: 16,
              display: 'grid',
              gridTemplateColumns: '70px 1fr',
              gap: 12,
            }}
          >
            <div
              className='lbl'
              style={{ alignSelf: 'flex-start', paddingTop: 6 }}
            >
              {tr('console.playground.system', 'system')}
            </div>
            <textarea
              data-testid='playground-system'
              value={form.system}
              onChange={(e) => update('system')(e.target.value)}
              rows={2}
              style={{
                width: '100%',
                fontFamily: 'var(--hf-mono)',
                fontSize: 12,
                padding: '6px 10px',
                border: '1px solid var(--hf-rule)',
                background: 'var(--hf-sunken)',
                color: 'var(--hf-ink)',
                borderRadius: 2,
                resize: 'vertical',
                outline: 'none',
              }}
            />
            <div
              className='lbl'
              style={{ alignSelf: 'flex-start', paddingTop: 6 }}
            >
              {tr('console.playground.user', 'user')}
            </div>
            <textarea
              data-testid='playground-user'
              value={form.user}
              onChange={(e) => update('user')(e.target.value)}
              rows={3}
              style={{
                width: '100%',
                fontFamily: 'var(--hf-mono)',
                fontSize: 13,
                padding: '6px 10px',
                border: '1px solid var(--hf-rule)',
                background: 'var(--hf-sunken)',
                color: 'var(--hf-ink)',
                borderRadius: 2,
                resize: 'vertical',
                outline: 'none',
              }}
            />
          </div>

          <div
            style={{
              display: 'flex',
              gap: 8,
              marginTop: 14,
              alignItems: 'center',
              flexWrap: 'wrap',
            }}
          >
            <span className='lbl'>
              {tr('console.playground.params', 'params')}
            </span>
            <label className='pill' style={{ display: 'inline-flex', gap: 6 }}>
              {tr('console.playground.param_temp', 'temp')} ·
              <input
                data-testid='playground-temp'
                type='number'
                min='0'
                max='2'
                step='0.1'
                value={form.temperature}
                onChange={(e) => update('temperature')(e.target.value)}
                style={{
                  width: 48,
                  border: 'none',
                  background: 'transparent',
                  color: 'inherit',
                  outline: 'none',
                  fontFamily: 'var(--hf-mono)',
                }}
              />
            </label>
            <label className='pill' style={{ display: 'inline-flex', gap: 6 }}>
              top_p ·
              <input
                data-testid='playground-topp'
                type='number'
                min='0'
                max='1'
                step='0.05'
                value={form.top_p}
                onChange={(e) => update('top_p')(e.target.value)}
                style={{
                  width: 48,
                  border: 'none',
                  background: 'transparent',
                  color: 'inherit',
                  outline: 'none',
                  fontFamily: 'var(--hf-mono)',
                }}
              />
            </label>
            <label className='pill' style={{ display: 'inline-flex', gap: 6 }}>
              {tr('console.playground.param_max', 'max')} ·
              <input
                data-testid='playground-max'
                type='number'
                min='1'
                max='8192'
                step='128'
                value={form.max_tokens}
                onChange={(e) => update('max_tokens')(e.target.value)}
                style={{
                  width: 64,
                  border: 'none',
                  background: 'transparent',
                  color: 'inherit',
                  outline: 'none',
                  fontFamily: 'var(--hf-mono)',
                }}
              />
            </label>
            <span style={{ flex: 1 }} />
            <span className='kbd'>⌘</span>
            <span className='kbd'>↵</span>
            <button
              type='button'
              className='btn acc'
              disabled={running}
              onClick={runAll}
              data-testid='playground-run'
            >
              {running
                ? tr('console.playground.running', '▶ running…')
                : tr('console.playground.run_all', '▶ run all {{count}}', {
                    count: form.models.length,
                  })}
            </button>
          </div>
        </div>

        {/* ── Model header strip ── */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `repeat(${form.models.length}, 1fr)`,
            borderBottom: '1px solid var(--hf-rule)',
          }}
        >
          {displayCols.map((col) => {
            const v = col.result
              ? verdictFor(col.result, items || [])
              : { label: '', color: 'var(--hf-ink-2)' };
            return (
              <div
                key={col.idx}
                style={{
                  padding: 16,
                  borderRight:
                    col.idx < form.models.length - 1
                      ? '1px solid var(--hf-rule)'
                      : 0,
                  background: 'var(--hf-paper)',
                }}
              >
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'baseline',
                    justifyContent: 'space-between',
                  }}
                >
                  <div>
                    <div className='display' style={{ fontSize: 17 }}>
                      {col.model}
                    </div>
                    <div className='faint mono' style={{ fontSize: 10 }}>
                      {col.vendor}
                    </div>
                  </div>

                  {/* swap▾ — toggles models in the form.models array */}
                  <div
                    ref={swapOpen === col.idx ? swapRef : null}
                    style={{ position: 'relative' }}
                  >
                    <button
                      type='button'
                      className='btn ghost sm'
                      data-testid={`playground-swap-btn-${col.idx}`}
                      onClick={() => handleSwapToggle(col.idx)}
                    >
                      {tr('console.playground.swap_btn', 'swap ▾')}
                    </button>
                    {swapOpen === col.idx && (
                      <div
                        data-testid={`playground-swap-dropdown-${col.idx}`}
                        style={{
                          position: 'absolute',
                          top: '100%',
                          right: 0,
                          zIndex: 100,
                          background: 'var(--hf-paper)',
                          border: '1px solid var(--hf-rule)',
                          borderRadius: 4,
                          minWidth: 200,
                          maxHeight: 260,
                          overflowY: 'auto',
                          boxShadow: '0 4px 12px rgba(0,0,0,0.12)',
                          padding: '4px 0',
                        }}
                      >
                        {availableModels.length === 0 && (
                          <div
                            style={{
                              padding: '6px 12px',
                              color: 'var(--hf-ink-2)',
                              fontSize: 11,
                            }}
                          >
                            {tr('console.common.loading', 'loading…')}
                          </div>
                        )}
                        {availableModels.map((m) => {
                          const selected = form.models.includes(m);
                          return (
                            <button
                              key={m}
                              type='button'
                              className='btn ghost sm'
                              data-testid={`playground-swap-model-${m}`}
                              onClick={() => {
                                toggleModel(m);
                                setSwapOpen(null);
                              }}
                              style={{
                                display: 'block',
                                width: '100%',
                                textAlign: 'left',
                                padding: '6px 12px',
                                fontWeight: selected ? 700 : 400,
                              }}
                            >
                              {selected ? '✓ ' : ''}
                              {m}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </div>
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                  {col.result ? (
                    <>
                      <span className='pill'>
                        <span
                          className={`dot ${
                            col.result.error_code ? 'err' : 'ok'
                          }`}
                        />
                        {col.result.latency_ms}ms
                      </span>
                      <span className='pill'>
                        {tr('console.playground.unit_tok', 'tok')}{' '}
                        {col.result.prompt_tokens}↗
                        {col.result.completion_tokens}
                      </span>
                    </>
                  ) : (
                    <span className='pill faint'>
                      {running
                        ? tr('console.playground.running_short', 'running…')
                        : tr('console.playground.idle', 'idle')}
                    </span>
                  )}
                </div>
                {v.label && (
                  <div
                    className='lbl'
                    style={{ marginTop: 10, color: v.color }}
                  >
                    {tr(`console.playground.verdict_${v.label}`, v.label)}
                  </div>
                )}
              </div>
            );
          })}
        </div>

        {/* ── Output panels ── */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `repeat(${form.models.length}, 1fr)`,
            overflow: 'hidden',
          }}
        >
          {displayCols.map((col) => (
            <div
              key={col.idx}
              data-testid={`playground-col-${col.idx}`}
              style={{
                padding: 18,
                borderRight:
                  col.idx < form.models.length - 1
                    ? '1px solid var(--hf-rule)'
                    : 0,
                overflow: 'auto',
                fontSize: 13,
                lineHeight: 1.6,
                color: col.result?.error_code
                  ? 'var(--hf-err)'
                  : 'var(--hf-ink-2)',
                whiteSpace: 'pre-wrap',
              }}
            >
              {!col.result && running && (
                <span className='muted'>
                  {tr('console.playground.awaiting', 'Awaiting response…')}
                </span>
              )}
              {!col.result && !running && (
                <span className='muted'>
                  {tr(
                    'console.playground.press_hint',
                    'Press ⌘/Ctrl + Enter or click "run all" to compare.',
                  )}
                </span>
              )}
              {col.result?.error_code && (
                <>
                  <div className='strong'>
                    {tr('console.playground.error_label', 'Error')} ·{' '}
                    {col.result.error_code}
                  </div>
                  <div style={{ marginTop: 6 }}>
                    {col.result.error_message ||
                      tr('console.playground.no_message', '(no message)')}
                  </div>
                </>
              )}
              {col.result && !col.result.error_code && col.result.content}
            </div>
          ))}
        </div>

        {/* ── Footer (token usage + copy) ── */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `repeat(${form.models.length}, 1fr)`,
            borderTop: '1px solid var(--hf-rule)',
            background: 'var(--hf-paper)',
          }}
        >
          {displayCols.map((col) => (
            <div
              key={col.idx}
              style={{
                padding: '10px 16px',
                borderRight:
                  col.idx < form.models.length - 1
                    ? '1px solid var(--hf-rule)'
                    : 0,
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                fontSize: 11,
              }}
            >
              <span className='muted mono'>
                {col.result
                  ? `${col.result.prompt_tokens + col.result.completion_tokens} ${tr('console.playground.unit_tok', 'tok')}`
                  : '—'}
              </span>
              <span style={{ flex: 1 }} />
              <button
                type='button'
                className='btn ghost sm'
                disabled={!col.result?.content}
                onClick={() => {
                  if (!col.result?.content) return;
                  navigator.clipboard?.writeText(col.result.content);
                }}
              >
                {tr('console.common.copy', 'copy')}
              </button>
            </div>
          ))}
        </div>
      </div>
    </HFShell>
  );
};

export default HFPlayground;
