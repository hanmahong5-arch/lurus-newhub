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
import HfMarkdown from '../../../components/hifi/HfMarkdown';
import { API, showError, showSuccess } from '../../../helpers';
import { useFormDraft } from '../../../hooks/common/useFormDraft';
import { useTenantRead } from '../../../hooks/common/useTenantRead';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import {
  useRoutableModels,
  defaultCompareModels,
  WIRE_ANTHROPIC,
} from '../../../hooks/models/useRoutableModels';
import { buildCatalog, fmtUsd } from '../Models/catalog';
import {
  estimateCost,
  indexCatalogByModel,
  readURLParams,
  verdictFor,
} from './cost';
import ViewCodePanel from './ViewCodePanel';

// Above this many routable models, the swap▾ dropdown grows a filter input.
const SWAP_FILTER_THRESHOLD = 8;

// Repeated inline style shapes, hoisted so each usage site is one line.
const alertBannerStyle = (borderColor) => ({
  marginTop: 10,
  fontSize: 11,
  padding: '4px 10px',
  borderLeft: `2px solid ${borderColor}`,
  background: 'var(--hf-warn-bg)',
  color: 'var(--hf-ink-2)',
});
const dropdownMenuStyle = (extra) => ({
  position: 'absolute',
  top: '100%',
  zIndex: 100,
  background: 'var(--hf-paper)',
  border: '1px solid var(--hf-rule)',
  borderRadius: 4,
  boxShadow: '0 4px 12px rgba(0,0,0,0.12)',
  padding: '4px 0',
  ...extra,
});
const dropdownItemStyle = (extra) => ({
  display: 'block',
  width: '100%',
  textAlign: 'left',
  padding: '6px 12px',
  ...extra,
});
const dropdownHintStyle = {
  padding: '6px 12px',
  color: 'var(--hf-ink-2)',
  fontSize: 11,
};
const paramPillInputStyle = (width) => ({
  width,
  border: 'none',
  background: 'transparent',
  color: 'inherit',
  outline: 'none',
  fontFamily: 'var(--hf-mono)',
});
const promptTextareaStyle = (fontSize) => ({
  width: '100%',
  fontFamily: 'var(--hf-mono)',
  fontSize,
  padding: '6px 10px',
  border: '1px solid var(--hf-rule)',
  background: 'var(--hf-sunken)',
  color: 'var(--hf-ink)',
  borderRadius: 2,
  resize: 'vertical',
  outline: 'none',
});

// HiFi 5 — Playground multi-model compare, POST .../playground/run. The
// compare draft/swap list/run guard all come from GET .../models/routable
// (routable-draft-sync effect below) — no literal model list anywhere.

const DEFAULT_FORM = {
  system: 'You are a helpful, concise assistant.',
  user: 'What is the capital of Australia? Briefly.',
  temperature: 0.7,
  top_p: 1.0,
  max_tokens: 1024,
  // Populated once routable resolves (draft-sync effect) — no literal list.
  models: [],
};

const HFPlayground = () => {
  const tenantSlug = useTenantSlug();
  const { t: tr } = useTranslation();

  // Persisted via useFormDraft; key NOT per-tenant (prompt reused across).
  const [form, setForm, clearDraft, , /* isDirty */ restoredFromDraft] =
    useFormDraft('playground-form', DEFAULT_FORM);
  const [running, setRunning] = useState(false);
  const [items, setItems] = useState(null); // null = never run; [] = ran with errors

  // Routing truth — fetched eagerly, draft-sync/run-guard need it sooner.
  const {
    items: routableModels,
    loading: modelsLoading,
    error: modelsError,
    resolved: modelsResolved,
  } = useRoutableModels(tenantSlug, { chatOnly: true });
  const availableModels = routableModels.map((m) => m.id).filter(Boolean);
  const vendorFor = (modelId) =>
    routableModels.find((m) => m.id === modelId)?.owned_by || '—';
  const noModelsRoutable = modelsResolved && routableModels.length === 0;

  // Same route + group_ratio lookup as Models/index.jsx — numbers agree.
  const { data: pricing } = useTenantRead(
    tenantSlug && `/api/v2/${tenantSlug}/pricing`,
    {
      parse: (d) => ({
        rows: d?.pricing || [],
        groupRatio: d?.group_ratio || {},
      }),
    },
  );
  const userGroup = useMemo(() => {
    try {
      return (
        JSON.parse(localStorage.getItem('user') || '{}').group || 'default'
      );
    } catch (_) {
      return 'default';
    }
  }, []);
  const catalogByModel = useMemo(
    () =>
      indexCatalogByModel(
        buildCatalog({
          routable: routableModels,
          pricing: pricing?.rows ?? [],
          groupRatio: pricing?.groupRatio?.[userGroup] ?? 1,
        }),
      ),
    [routableModels, pricing, userGroup],
  );

  // "view code" — expands ViewCodePanel in place, filled from column 0.
  const [viewCodeOpen, setViewCodeOpen] = useState(false);

  // URL prefill; prefillModelRef tells draft-sync below WHY draft changed.
  const urlPrefillApplied = useRef(false);
  const prefillModelRef = useRef(null);
  const [prefillUnroutableHint, setPrefillUnroutableHint] = useState(null);
  useEffect(() => {
    if (urlPrefillApplied.current) return;
    urlPrefillApplied.current = true;
    const prefill = readURLParams(window.location.search);
    if (prefill.models && prefill.models.length === 1) {
      prefillModelRef.current = prefill.models[0];
    }
    if (Object.keys(prefill).length > 0) {
      setForm((prev) => ({ ...prev, ...prefill }));
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Drop draft models no longer routable; fall back to defaultCompareModels.
  useEffect(() => {
    if (!modelsResolved) return;
    const validIds = new Set(routableModels.map((m) => m.id));
    if (prefillModelRef.current && !validIds.has(prefillModelRef.current)) {
      setPrefillUnroutableHint(prefillModelRef.current);
    }
    prefillModelRef.current = null; // only check once, right after resolve
    setForm((prev) => {
      const kept = prev.models.filter((m) => validIds.has(m));
      // An empty draft (0 === 0) must still fall through to default-fill.
      if (kept.length === prev.models.length && prev.models.length > 0) {
        return prev;
      }
      if (kept.length > 0) return { ...prev, models: kept };
      return {
        ...prev,
        models: defaultCompareModels(routableModels).map((m) => m.id),
      };
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelsResolved, routableModels]);

  // ── Preset state ──
  const [presets, setPresets] = useState(null); // null = not yet loaded
  const [blankOpen, setBlankOpen] = useState(false);
  const [swapOpen, setSwapOpen] = useState(null); // colIdx | null
  const [swapFilter, setSwapFilter] = useState('');
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

  // Shared run/save/share/view-code params: string form fields → numbers.
  const currentParams = useCallback(
    () => ({
      temperature: Number(form.temperature),
      top_p: Number(form.top_p),
      max_tokens: Math.max(1, parseInt(form.max_tokens, 10) || 1024),
    }),
    [form.temperature, form.top_p, form.max_tokens],
  );

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
    if (noModelsRoutable || form.models.length === 0) return;
    setRunning(true);
    setItems(null);
    try {
      const res = await API.post(`/api/v2/${tenantSlug}/playground/run`, {
        system: form.system,
        user: form.user,
        models: form.models,
        params: currentParams(),
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
  }, [form, tenantSlug, tr, noModelsRoutable, currentParams]);

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
        params: JSON.stringify(currentParams()),
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
  }, [form, tenantSlug, tr, currentParams]);

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
      params.set('params', JSON.stringify(currentParams()));
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
  }, [form, tr, currentParams]);

  // ── swap▾ toggle handler ──
  const handleSwapToggle = useCallback(
    (colIdx) => {
      if (swapOpen === colIdx) {
        setSwapOpen(null);
        return;
      }
      setSwapOpen(colIdx);
      setSwapFilter('');
      setBlankOpen(false);
    },
    [swapOpen],
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

  const swapFilterActive = availableModels.length > SWAP_FILTER_THRESHOLD;
  const swapFilteredModels = swapFilterActive
    ? availableModels.filter((m) =>
        m.toLowerCase().includes(swapFilter.trim().toLowerCase()),
      )
    : availableModels;

  const onSwapFilterKeyDown = useCallback(
    (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        if (swapFilteredModels[0]) {
          toggleModel(swapFilteredModels[0]);
          setSwapOpen(null);
        }
      } else if (e.key === 'Escape') {
        setSwapOpen(null);
      }
    },
    [swapFilteredModels, toggleModel],
  );

  const displayCols = form.models.map((m, i) => {
    const found = items?.[i];
    const result = found || null;
    // Only from a completed, successful run's own token counts.
    const cost =
      result && !result.error_code
        ? estimateCost(
            catalogByModel.get(m),
            result.prompt_tokens,
            result.completion_tokens,
          )
        : null;
    return {
      idx: i,
      model: m,
      vendor: vendorFor(m),
      result,
      cost,
    };
  });

  // Shared by the 3 N-column grids below and each column's own right rule.
  const colsGridStyle = (extra) => ({
    display: 'grid',
    gridTemplateColumns: `repeat(${form.models.length}, 1fr)`,
    ...extra,
  });
  const colBorderRight = (idx) =>
    idx < form.models.length - 1 ? '1px solid var(--hf-rule)' : 0;

  const codeColModel = form.models[0];
  const codeColAnthropicModel = routableModels.some(
    (m) =>
      m.id === codeColModel &&
      m.supported_endpoint_types?.includes(WIRE_ANTHROPIC),
  )
    ? codeColModel
    : undefined;

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
                style={dropdownMenuStyle({ left: 0, minWidth: 180 })}
              >
                <button
                  type='button'
                  className='btn ghost sm'
                  data-testid='playground-blank-clear'
                  onClick={loadBlank}
                  style={dropdownItemStyle()}
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
                        style={dropdownItemStyle()}
                      >
                        {p.name}
                      </button>
                    ))}
                  </div>
                )}
                {presets && presets.length === 0 && (
                  <div style={dropdownHintStyle}>
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
              style={alertBannerStyle('var(--hf-warn)')}
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

          {prefillUnroutableHint && (
            <div
              style={alertBannerStyle('var(--hf-warn)')}
              data-testid='playground-prefill-unroutable'
            >
              {tr(
                'console.playground.prefill_unroutable',
                'Model "{{model}}" is not routable for this tenant — showing the default compare set instead.',
                { model: prefillUnroutableHint },
              )}
            </div>
          )}

          {modelsError && (
            <div
              style={alertBannerStyle('var(--hf-err)')}
              data-testid='playground-models-error'
            >
              {tr(
                'console.playground.models_load_failed',
                'failed to load models',
              )}
            </div>
          )}

          {!modelsError && noModelsRoutable && (
            <div
              style={alertBannerStyle('var(--hf-err)')}
              data-testid='playground-no-models'
            >
              {tr('console.playground.no_models', 'no models available')}
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
              style={promptTextareaStyle(12)}
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
              style={promptTextareaStyle(13)}
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
                style={paramPillInputStyle(48)}
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
                style={paramPillInputStyle(48)}
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
                style={paramPillInputStyle(64)}
              />
            </label>
            <span style={{ flex: 1 }} />
            <button
              type='button'
              className='btn ghost'
              disabled={!codeColModel}
              onClick={() => setViewCodeOpen((o) => !o)}
              data-testid='playground-view-code-btn'
            >
              {tr('console.playground.view_code', 'view code')}
            </button>
            <span className='kbd'>⌘</span>
            <span className='kbd'>↵</span>
            <button
              type='button'
              className='btn acc'
              disabled={running || noModelsRoutable || form.models.length === 0}
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
          {viewCodeOpen && codeColModel && (
            <ViewCodePanel
              model={codeColModel}
              anthropicModel={codeColAnthropicModel}
              system={form.system}
              user={form.user}
              params={currentParams()}
            />
          )}
        </div>

        {/* ── Model header strip ── */}
        <div
          style={colsGridStyle({ borderBottom: '1px solid var(--hf-rule)' })}
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
                  borderRight: colBorderRight(col.idx),
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
                        style={dropdownMenuStyle({
                          right: 0,
                          minWidth: 200,
                          maxHeight: 260,
                          overflowY: 'auto',
                        })}
                      >
                        {swapFilterActive && (
                          <input
                            type='text'
                            autoFocus
                            data-testid='playground-swap-filter'
                            placeholder={tr(
                              'console.playground.swap_filter',
                              'filter models',
                            )}
                            value={swapFilter}
                            onChange={(e) => setSwapFilter(e.target.value)}
                            onKeyDown={onSwapFilterKeyDown}
                            style={{
                              ...promptTextareaStyle(11),
                              width: 'calc(100% - 16px)',
                              margin: '2px 8px 6px',
                              padding: '5px 8px',
                              resize: 'none',
                            }}
                          />
                        )}
                        {/* loading → error → empty (filter-empty and
                            nothing-routable share one honest empty state). */}
                        {(modelsLoading ||
                          modelsError ||
                          swapFilteredModels.length === 0) && (
                          <div
                            data-testid={
                              modelsLoading
                                ? 'playground-swap-loading'
                                : modelsError
                                  ? 'playground-swap-error'
                                  : 'playground-swap-empty'
                            }
                            style={
                              modelsError
                                ? {
                                    ...dropdownHintStyle,
                                    color: 'var(--hf-err)',
                                  }
                                : dropdownHintStyle
                            }
                          >
                            {modelsLoading
                              ? tr('console.common.loading', 'loading…')
                              : modelsError
                                ? tr(
                                    'console.playground.models_load_failed',
                                    'failed to load models',
                                  )
                                : tr(
                                    'console.playground.no_models',
                                    'no models available',
                                  )}
                          </div>
                        )}
                        {!modelsLoading &&
                          !modelsError &&
                          swapFilteredModels.map((m) => {
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
                                style={dropdownItemStyle({
                                  fontWeight: selected ? 700 : 400,
                                })}
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
                      {/* Absent, never a fabricated $0 — see estimateCost. */}
                      {col.cost != null && (
                        <span
                          className='pill'
                          data-testid={`playground-est-cost-${col.idx}`}
                          title={tr(
                            'console.playground.est_cost_hint',
                            'Estimated from your current price list; your bill is authoritative.',
                          )}
                        >
                          {tr('console.playground.est_cost', 'est. {{cost}}', {
                            cost: fmtUsd(col.cost),
                          })}
                        </span>
                      )}
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
        <div style={colsGridStyle({ overflow: 'hidden' })}>
          {displayCols.map((col) => (
            <div
              key={col.idx}
              data-testid={`playground-col-${col.idx}`}
              style={{
                padding: 18,
                borderRight: colBorderRight(col.idx),
                overflow: 'auto',
                fontSize: 13,
                lineHeight: 1.6,
                color: col.result?.error_code
                  ? 'var(--hf-err)'
                  : 'var(--hf-ink-2)',
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
              {/* Error text is plain, never markdown. */}
              {col.result?.error_code && (
                <>
                  <div className='strong'>
                    {tr('console.playground.error_label', 'Error')} ·{' '}
                    {col.result.error_code}
                  </div>
                  <div style={{ marginTop: 6, whiteSpace: 'pre-wrap' }}>
                    {col.result.error_message ||
                      tr('console.playground.no_message', '(no message)')}
                  </div>
                </>
              )}
              {col.result && !col.result.error_code && (
                <HfMarkdown
                  content={col.result.content}
                  testId={`playground-output-${col.idx}`}
                />
              )}
            </div>
          ))}
        </div>

        {/* ── Footer (token usage + copy) ── */}
        <div
          style={colsGridStyle({
            borderTop: '1px solid var(--hf-rule)',
            background: 'var(--hf-paper)',
          })}
        >
          {displayCols.map((col) => (
            <div
              key={col.idx}
              style={{
                padding: '10px 16px',
                borderRight: colBorderRight(col.idx),
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
