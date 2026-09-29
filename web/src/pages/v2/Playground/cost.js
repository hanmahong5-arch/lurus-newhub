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

// estimateCost — pure function, no React, no fetch. Turns a
// Models/catalog.js entry (inputPerM / outputPerM / perCall, already
// carrying the caller's group_ratio — see buildCatalog's own comment) plus
// a run's token counts into an estimated $ figure for Playground's per-column
// latency pill.
//
// Returns null — never 0 — whenever the number would be a guess dressed up
// as a fact: no catalog entry, an entry this tenant cannot see a price for,
// or (for ratio billing) token counts the run hasn't reported yet. A
// genuinely free/zero-cost model still returns 0, which is a real number,
// not a fabricated one — the caller decides whether "$0" is worth showing;
// this function's job is only to never manufacture a number nothing backs.
export function estimateCost(entry, promptTokens, completionTokens) {
  if (!entry) return null;
  if (entry.quotaType === 1) {
    // Per-call billing — token counts are irrelevant, the price is flat.
    return entry.perCall == null ? null : entry.perCall;
  }
  if (entry.inputPerM == null || entry.outputPerM == null) return null;
  if (
    typeof promptTokens !== 'number' ||
    typeof completionTokens !== 'number'
  ) {
    return null;
  }
  return (
    (promptTokens / 1e6) * entry.inputPerM +
    (completionTokens / 1e6) * entry.outputPerM
  );
}

/** Catalog entries keyed by model id, for O(1) lookup per Playground column. */
export function indexCatalogByModel(entries) {
  const byModel = new Map();
  for (const e of entries || []) {
    if (e?.id) byModel.set(e.id, e);
  }
  return byModel;
}

// readURLParams — extracts ?prompt=&prefill_model=&models=&params= from a
// URLSearchParams-compatible search string, for URL-self-contained share
// links and the Models page's "try ↗" link. prefill_model (a single model
// id) takes precedence over `models` when both are present, since "try" is
// a more specific intent than a restored multi-model share link. Moved out
// of index.jsx (cycle-19 L3) purely to stay under that file's line ceiling
// — still Playground-only, still pure.
export function readURLParams(search) {
  try {
    const params = new URLSearchParams(search);
    const out = {};
    if (params.has('prompt')) out.user = params.get('prompt');
    if (params.has('prefill_model')) {
      const pm = params.get('prefill_model');
      if (pm) out.models = [pm];
    } else if (params.has('models')) {
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

// verdictFor — sorts a run's columns by latency (fastest leftmost is the
// caller's job, this just labels); stable via original index as tiebreaker.
// `label` doubles as the i18n key suffix (console.playground.verdict_<label>).
export function verdictFor(item, allItems) {
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
}
