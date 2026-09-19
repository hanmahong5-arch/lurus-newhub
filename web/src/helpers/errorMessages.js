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
  const errorCode = error?.response?.data?.error_code;
  if (errorCode === 'SESSION_REGISTRY_DISABLED') {
    return t('console.settings.session_registry_disabled');
  }
  // 403 USER_DISABLED: a banned account. The 403 branch below shows the
  // generic "no permission" copy, which tells a banned admin the wrong
  // thing; the backend minted this code so the console could branch.
  if (errorCode === 'USER_DISABLED') {
    return t('console.errors.account_disabled');
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
