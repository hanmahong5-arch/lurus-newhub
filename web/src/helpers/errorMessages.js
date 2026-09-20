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

// resolveErrorMessage turns an axios error (or anything thrown) into a calm,
// localized, user-facing string. Internal/technical detail is intentionally
// dropped — the backend message is for logs, not the end user.
export function resolveErrorMessage(error) {
  const t = i18n.t.bind(i18n);
  const status = error?.response?.status;

  // Network / no-response: request never reached a server (offline, DNS,
  // CORS preflight, connection reset).
  if (error?.isAxiosError && !error.response) {
    return t('console.errors.network');
  }

  // error_code first, for the few refusals where the status alone is not
  // enough to say anything useful. Cycle-12 L4: the session endpoints answer
  // 409 SESSION_REGISTRY_DISABLED when per-device sessions are not enabled on
  // this deployment — without this the user saw the backend's raw English
  // sentence in a zh-default console, because 409 falls through to the
  // backend-message branch at the bottom.
  //
  // Every branch below calls t() with a literal key (not a variable) on
  // purpose — src/i18n/i18n-integrity.test.js scans for exactly that shape
  // to prove every key it can find is present in en.json; a variable lookup
  // (e.g. a code->key map) is invisible to that scan and requires a separate
  // entry in its NON_LITERAL_T_CALLS allowlist, a file this lane does not
  // own. Cycle-13 L3 added the TOKEN_*/REDEMPTION_* cases: the API contract
  // now requires error.message be English-only (or, for redemption, the
  // pre-existing Chinese text the Switch desktop client greps substrings out
  // of — neither is meant for a zh-default console reader), so these route
  // through i18n instead of falling to the backend-message branch the way
  // SESSION_REGISTRY_DISABLED/USER_DISABLED already did.
  const errorCode = error?.response?.data?.error_code;
  switch (errorCode) {
    case 'SESSION_REGISTRY_DISABLED':
      return t('console.settings.session_registry_disabled');
    case 'USER_DISABLED':
      return t('console.errors.account_disabled');
    case 'TOKEN_NAME_INVALID':
      return t('console.errors.token_name_invalid');
    case 'TOKEN_QUOTA_INVALID':
      return t('console.errors.token_quota_invalid');
    case 'TOKEN_EXPIRY_INVALID':
      return t('console.errors.token_expiry_invalid');
    case 'TOKEN_RATE_LIMIT_INVALID':
      return t('console.errors.token_rate_limit_invalid');
    case 'TOKEN_SCOPE_INVALID':
      return t('console.errors.token_scope_invalid');
    case 'TOKEN_MODEL_LIMIT_INVALID':
      return t('console.errors.token_model_limit_invalid');
    case 'TOKEN_ENABLE_REJECTED':
      return t('console.errors.token_enable_rejected');
    case 'REDEMPTION_INVALID':
      return t('console.errors.redemption_invalid');
    case 'REDEMPTION_USED':
      return t('console.errors.redemption_used');
    case 'REDEMPTION_EXPIRED':
      return t('console.errors.redemption_expired');
    case 'REDEMPTION_TENANT_MISMATCH':
      return t('console.errors.redemption_tenant_mismatch');
    case 'REDEMPTION_FAILED':
      return t('console.errors.redemption_failed');
    default:
      break;
  }

  switch (status) {
    case 401:
      // Session expired or not yet bridged. After the WS-A auth fix this is
      // rare; when it happens we say it plainly without any internal jargon.
      return t('console.errors.session_expired');
    case 403:
      return t('console.errors.forbidden');
    case 404:
      return t('console.errors.not_found');
    case 429:
      return t('console.errors.rate_limited');
    case 500:
    case 502:
    case 503:
    case 504:
      return t('console.errors.server');
    default:
      break;
  }

  // Fall back to the backend's human message when present and non-technical,
  // else a generic calm line. We still prefer the mapped copy above.
  const backendMsg = error?.response?.data?.message;
  if (typeof backendMsg === 'string' && backendMsg.trim()) {
    return backendMsg;
  }
  if (error?.message) {
    return error.message;
  }
  return t('console.errors.unknown');
}

// True 401 with no client-side notion of being logged in → the user really
// is signed out and should be sent to login (vs. a transient session blip
// while a localStorage `user` shim still exists).
export function isHardUnauthorized(error) {
  return error?.response?.status === 401 && !localStorage.getItem('user');
}

// ── Toast de-duplication ────────────────────────────────────────────────
// A small time-windowed set keyed by the resolved message. The first
// occurrence within the window passes; identical repeats are suppressed.
const DEDUP_WINDOW_MS = 1500;
const recent = new Map(); // message -> expiry timestamp (ms)

// shouldEmit returns true at most once per `message` per DEDUP_WINDOW_MS.
// `nowMs` is injectable for tests.
export function shouldEmit(message, nowMs = Date.now()) {
  const expiry = recent.get(message);
  if (expiry && expiry > nowMs) {
    return false;
  }
  recent.set(message, nowMs + DEDUP_WINDOW_MS);
  // Opportunistic cleanup so the map can't grow unbounded on a long session.
  for (const [key, exp] of recent) {
    if (exp <= nowMs) recent.delete(key);
  }
  return true;
}

// Test seam — clear the dedup window between cases.
export function _resetDedup() {
  recent.clear();
}
