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

// Daily, per-model series for the activity chart — pure, no React.
//
// Input rows are GET /api/data/self/ quota_data rows: one per (user, model,
// hour) with quota, token_used and count (internal/domain/entity/usedata.go).

// metric → row field
export const METRIC_FIELD = {
  spend: 'quota',
  tokens: 'token_used',
  requests: 'count',
};

export const OTHER = '__other__';

// Buckets key on the BROWSER's local calendar day, not the UTC day: labels
// render in local time, and keying on UTC days would move a UTC+8 reader's
// 00:00–08:00 traffic into the previous day's bar. Walking the window with
// setDate (not a fixed 86 400 s step) stays correct across DST.
export const localDayKey = (tsSeconds) => {
  const d = new Date(tsSeconds * 1000);
  d.setHours(0, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
};

// Local clock hour (not unix-hour floor: a UTC+5:30 reader's hours start
// at :30 in unix time).
export const localHourKey = (tsSeconds) => {
  const d = new Date(tsSeconds * 1000);
  d.setMinutes(0, 0, 0);
  return Math.floor(d.getTime() / 1000);
};

/**
 * @param {Array} rows
 * @param {object} o
 * @param {number} o.end       window end (unix seconds)
 * @param {number} [o.days]    how many local days, ending with o.end's day
 * @param {number} [o.hours]   OR how many local clock hours, ending with
 *                             o.end's hour — the rows are hourly, so a 24h
 *                             view needs no second fetch
 * @param {'spend'|'tokens'|'requests'} [o.metric='spend']
 * @param {number} [o.topN=6]  models drawn individually; the rest fold into OTHER
 * @returns {{days: Array<{day:number,total:number,parts:Object<string,number>}>,
 *            series: Array<{model:string,total:number}>, total:number,
 *            unit: 'day'|'hour'}}
 *   `day` is the bucket start (a local day or hour) whatever the unit.
 */
export function buildActivity(
  rows,
  { end, days, hours, metric = 'spend', topN = 6 },
) {
  const field = METRIC_FIELD[metric] || 'quota';
  const unit = hours ? 'hour' : 'day';
  const keyOf = unit === 'hour' ? localHourKey : localDayKey;
  const lastKey = keyOf(end);
  const cursor = new Date(lastKey * 1000);
  if (unit === 'hour') cursor.setHours(cursor.getHours() - (hours - 1));
  else cursor.setDate(cursor.getDate() - (days - 1));
  const firstKey = Math.floor(cursor.getTime() / 1000);

  const byBucket = new Map();
  const byModel = new Map();
  for (const row of rows || []) {
    const ts = Number(row?.created_at) || 0;
    if (!ts) continue;
    const key = keyOf(ts);
    if (key < firstKey || key > lastKey) continue;
    const v = Number(row?.[field]) || 0;
    if (!v) continue;
    const model = row?.model_name || '—';
    byModel.set(model, (byModel.get(model) || 0) + v);
    if (!byBucket.has(key)) byBucket.set(key, new Map());
    const m = byBucket.get(key);
    m.set(model, (m.get(model) || 0) + v);
  }

  const ranked = Array.from(byModel, ([model, total]) => ({
    model,
    total,
  })).sort((a, b) => b.total - a.total || a.model.localeCompare(b.model));
  const shown = ranked.slice(0, topN);
  const rest = ranked.slice(topN);
  const keep = new Set(shown.map((s) => s.model));
  const series = rest.length
    ? [...shown, { model: OTHER, total: rest.reduce((s, r) => s + r.total, 0) }]
    : shown;

  const out = [];
  const walk = new Date(firstKey * 1000);
  for (let key = firstKey; key <= lastKey; ) {
    const parts = {};
    let total = 0;
    for (const [model, v] of byBucket.get(key) || []) {
      const k = keep.has(model) ? model : OTHER;
      parts[k] = (parts[k] || 0) + v;
      total += v;
    }
    out.push({ day: key, total, parts });
    if (unit === 'hour') walk.setHours(walk.getHours() + 1);
    else walk.setDate(walk.getDate() + 1);
    key = Math.floor(walk.getTime() / 1000);
  }

  return {
    days: out,
    series,
    total: ranked.reduce((s, r) => s + r.total, 0),
    unit,
  };
}

/** A "nice" axis ceiling ≥ v: 1, 2, 2.5, 5 × 10^k. */
export function niceCeil(v) {
  if (!(v > 0)) return 0;
  const exp = Math.floor(Math.log10(v));
  const base = 10 ** exp;
  for (const m of [1, 2, 2.5, 5, 10]) {
    if (m * base >= v) return m * base;
  }
  return 10 * base;
}
