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

// row.js — how one log row is READ: its outcome class, its routing trace, its
// auxiliary `other` payload and the settlement marker on it. Extracted from
// pages/v2/Log/index.jsx by the cycle-13 wiring pass as a pure move: every
// function here is byte-identical to the one that stood there.
// web/src/file_size_ratchet.test.js holds index.jsx at its measured line
// count, so this cycle's additions to that page are paid for by an extraction
// rather than by raising the ceiling.
//
// None of it renders anything, which is the reason these five travel
// together: they are the page's interpretation of a row the API returned, and
// every one of them has to treat an absent or corrupt payload as "nothing to
// show" rather than as an error — a log page that throws on one malformed row
// is worse than one that renders it plainly.

// Log type 5 is LogTypeError (internal/domain/entity/log.go).
export const LOG_TYPE_ERROR = 5;

// Outcome derived from the log type — error logs (type 5) must not render as a
// green "200". We do not store the upstream HTTP status, so this reports the
// recorded outcome class, not a fabricated status code. `label` doubles as the
// i18n key suffix (console.log.outcome_<label>).
export const outcomeTag = (r) =>
  Number(r?.type) === LOG_TYPE_ERROR
    ? { cls: 'tag error', label: 'error' }
    : { cls: 'tag ok', label: 'ok' };

// Per-attempt routing trace, written by the relay only when a request bounced
// across channels (single-attempt requests carry none). It lives under
// other.admin_info, which the API strips for non-admin callers — so an empty
// list here means "not applicable or not visible to you", never an error.
export const parseRouteAttempts = (row) => {
  if (!row?.other) return [];
  try {
    const other =
      typeof row.other === 'string' ? JSON.parse(row.other) : row.other;
    const attempts = other?.admin_info?.route_attempts;
    return Array.isArray(attempts) ? attempts : [];
  } catch (_) {
    return [];
  }
};

export const attemptTagClass = (outcome) =>
  outcome === 'success'
    ? 'tag ok'
    : outcome === 'breaker_open'
      ? 'tag'
      : 'tag error';

// The row's auxiliary payload (tier-filtered server-side): user rows carry
// cache token counts and request_path; tenant-admin rows additionally carry
// admin_info. Absent or corrupt payloads read as null, never as an error.
export const parseOther = (row) => {
  if (!row?.other) return null;
  try {
    const o = typeof row.other === 'string' ? JSON.parse(row.other) : row.other;
    return o && typeof o === 'object' ? o : null;
  } catch (_) {
    return null;
  }
};

// L7: other.settlement is written by app.FlagSettlementOutcome
// (internal/app/settlement_outcome.go) only when the consume-quota
// settlement call for this row's request returned an error — the row still
// shows a price (the debit path is untouched), so this is the only signal
// on the row itself that the charge may not have actually landed.
export const isSettlementFailed = (row) =>
  parseOther(row)?.settlement === 'failed';
