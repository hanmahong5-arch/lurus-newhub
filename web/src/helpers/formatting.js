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

import i18n from '../i18n/i18n';

// Platform quota unit: 500_000 internal quota units == 1 USD. This is the
// DEFAULT, not a constant of nature — QuotaPerUnit is a server option, served
// to the browser as `quota_per_unit` on /api/status. Divide by the live value,
// never by this, or every figure on the page is wrong by whatever ratio the
// operator chose. The legacy console has always read the live value
// (helpers/render.jsx); the v2 pages each re-declared this literal instead.
export const QUOTA_PER_USD = 500_000;

/** The operator's configured quota-per-USD, falling back to the default. */
export function getQuotaPerUSD() {
  // Guard zero as well as NaN: a stored 0 would turn every amount into Infinity
  // rather than merely misreport it.
  const stored =
    typeof localStorage === 'undefined'
      ? NaN
      : parseFloat(localStorage.getItem('quota_per_unit'));
  return Number.isFinite(stored) && stored > 0 ? stored : QUOTA_PER_USD;
}

/**
 * Parse a dollar amount a person typed: "5", "5.00", "$5", " $1,000.50 ".
 * Returns null for an empty field or the "∞" the console shows for unlimited,
 * and NaN for anything that is not a finite, non-negative amount. Callers must
 * refuse NaN: the pages used to write `parseFloat(x) || 0`, so editing a cap
 * shown as "$2.00" into "$5.00" parsed as 0 and made the key unlimited.
 */
export function parseUSDInput(raw) {
  const s = String(raw ?? '')
    .trim()
    .replace(/^\$\s*/, '')
    .replace(/,/g, '');
  if (s === '' || s === '∞') return null;
  if (!/^\d+(\.\d+)?$|^\.\d+$/.test(s)) return NaN;
  return Number(s);
}

// Raw USD-equivalent of a quota amount, fixed to `digits` decimals (string).
export const quotaToUSD = (quota, digits = 2) =>
  ((quota || 0) / getQuotaPerUSD()).toFixed(digits);

// A wallet charge as recorded by the backend (logs.charged_cny4: integer
// 0.0001 CNY, the wallet's own precision), shown exactly, never re-derived.
export const formatCNY4 = (units4) =>
  units4 > 0 ? `¥${(units4 / 10_000).toFixed(4)}` : '—';

// USD cost with a `$` prefix; em-dash for zero/empty so tables stay quiet.
// A charge that really happened never renders as $0.0000: when it falls below
// the display precision it floors to the smallest representable amount. Showing
// a real debit as zero makes a metered product look free to the person reading
// the log — the legacy console has always floored this way
// (helpers/render.jsx renderQuota), the v2 pages did not.
export const formatUSD = (quota, digits = 4) => {
  if (!quota) return '—';
  const usd = quota / getQuotaPerUSD();
  const fixed = usd.toFixed(digits);
  if (usd > 0 && parseFloat(fixed) === 0) {
    return `$${Math.pow(10, -digits).toFixed(digits)}`;
  }
  return `$${fixed}`;
};

// CNY money with grouping and 2 decimals; em-dash for non-numeric input.
export const formatCNY = (v) =>
  typeof v === 'number'
    ? '¥' +
      v.toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      })
    : '—';

// Absolute timestamp (unix seconds) → locale date-time string.
export const formatTime = (ts) => {
  if (!ts || ts === 0) return '—';
  return new Date(ts * 1000).toLocaleString();
};

// Absolute timestamp (unix seconds) → "YYYY-MM-DD HH:mm:ss" in UTC. Only the
// audit trail uses this: its hash chain is verified against server-side
// timestamps, so the column is labelled "time (utc)" and must not follow the
// browser's zone the way every other page does.
export const formatTimeUTC = (unixSec) => {
  if (!unixSec) return '—';
  const d = new Date(unixSec * 1000);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toISOString().replace('T', ' ').slice(0, 19);
};

// Compact "Jun 15, 23:06" for dense lists (dashboard recent activity, news).
// Unix seconds like everything else here — the dashboard once took ms and was
// handed created_at in seconds, so every row read as January 1970.
export const formatShortTs = (unixSec) => {
  if (!unixSec) return '—';
  return new Date(unixSec * 1000).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
};

// Wall-clock HH:mm:ss.mmm — used by the live log tail where sub-second
// precision matters. Fixed en-GB so the 24h layout is stable across locales.
// `ms: false` drops the fraction for rows stored in whole seconds, where it
// would always read .000.
export const formatClockTime = (unixSec, { ms = true } = {}) => {
  if (!unixSec) return '—';
  return new Date(unixSec * 1000).toLocaleTimeString('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    ...(ms ? { fractionalSecondDigits: 3 } : {}),
  });
};

// Compact relative age ("just now" / "5m ago" / "3h ago" / "2d ago"),
// translated through console.common.time_*. `now` is injectable for testing.
export const formatRelativeTime = (
  unixSec,
  now = Math.floor(Date.now() / 1000),
) => {
  if (!unixSec) return '—';
  const t = i18n.t.bind(i18n);
  const diff = now - unixSec;
  if (diff < 60) return t('console.common.time_just_now');
  if (diff < 3600)
    return t('console.common.time_minutes_ago', {
      count: Math.floor(diff / 60),
    });
  if (diff < 86400)
    return t('console.common.time_hours_ago', {
      count: Math.floor(diff / 3600),
    });
  return t('console.common.time_days_ago', { count: Math.floor(diff / 86400) });
};
