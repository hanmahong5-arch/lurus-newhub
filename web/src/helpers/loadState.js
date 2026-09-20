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

// cycle-13 L7 (console honesty). A handful of v2 pages fetch on mount and
// then collapse each non-2xx outcome — a 500, a dropped connection, a
// 200 {success:false} permission refusal — into the same empty state a
// genuinely-empty account would show ("no traffic in last 5 min", "No users
// yet."). An operator whose session lapsed mid-day, or whose request hit a
// 502 during a rollout, reads that as "there is nothing here" instead of
// "we could not check". classifyLoad() gives every such call site one
// outcome vocabulary so a caller can render the three states separately.

// The two 200-with-{success:false} shapes this console can read as "you are
// not allowed", as opposed to "the request failed" or "a filter was
// rejected". `error_code` is the v2 envelope's machine-readable half
// (internal/adapter/middleware/admin_jwt_auth.go's denial table); the
// message string is the v1 session-auth branch's refusal
// (internal/adapter/middleware/auth.go, resolveSessionIdentity's
// `roleVal < minRole` branch — the literal lives there), which is
// part of the documented v1 response contract (switch consumes it), not
// incidental copy. Anything else that answers 200 {success:false} — a
// rejected filter window, a banned-user notice, a maintenance page — is
// classified 'error': claiming "Admin access required" for it would be a
// fresh false statement on the pages this module exists to make honest.
const PERMISSION_REFUSAL_MESSAGES = ['无权进行此操作，权限不足'];
const PERMISSION_REFUSAL_ERROR_CODE = 'PERMISSION_DENIED';

function isPermissionRefusalBody(body) {
  if (!body || typeof body !== 'object') return false;
  if (body.error_code === PERMISSION_REFUSAL_ERROR_CODE) return true;
  return PERMISSION_REFUSAL_MESSAGES.includes(
    String(body.message ?? '').trim(),
  );
}

/**
 * Classify one fetch outcome into 'ok' | 'forbidden' | 'unauthenticated' | 'error'.
 *
 * Accepts either shape a caller already has in hand:
 *   - a Promise.allSettled() entry: { status: 'fulfilled', value } or
 *     { status: 'rejected', reason } — value/reason is unwrapped first.
 *   - a raw axios outcome: the resolved response object (has `.data` at the
 *     top level) or the rejected error object (has `.response` instead).
 *
 * Mapping:
 *   - HTTP 401                                -> 'unauthenticated'
 *   - HTTP 403                                -> 'forbidden'
 *   - resolved response, body.success true    -> 'ok'
 *   - resolved response, body.success false, body recognisably a permission
 *     refusal (see isPermissionRefusalBody above)
 *                                             -> 'forbidden'
 *   - anything else (network error, 5xx, a thrown non-axios Error, a 200
 *     whose body failed for some other reason)
 *                                             -> 'error'
 *
 * The 'unauthenticated' bucket is deliberately folded back into an existing
 * one by every caller shipped in this cycle (Dashboard's single retry
 * banner, Users/Audit's error-with-retry panel, Diagnostics' forbidden
 * panel): a 401 on a console page normally means the session lapsed, and
 * the session-expiry redirect already belongs to the shared API
 * interceptor. It is a separate value here so the page that wants to say
 * "your session expired — sign in" can, without re-deriving it from the
 * HTTP status.
 *
 * @param {*} settledOrError
 * @returns {'ok'|'forbidden'|'unauthenticated'|'error'}
 */
export function classifyLoad(settledOrError) {
  let outcome = settledOrError;
  if (
    outcome &&
    typeof outcome === 'object' &&
    (outcome.status === 'fulfilled' || outcome.status === 'rejected') &&
    ('value' in outcome || 'reason' in outcome)
  ) {
    outcome = outcome.status === 'fulfilled' ? outcome.value : outcome.reason;
  }

  const httpStatus = outcome?.response?.status;
  if (httpStatus === 401) return 'unauthenticated';
  if (httpStatus === 403) return 'forbidden';

  // A resolved axios response carries `.data` at the top level; a rejected
  // axios error does not — its body, if any, lives at `.response.data`. That
  // asymmetry is what distinguishes "the call answered" from "the call
  // failed" here, not typeof or truthiness.
  if (outcome && typeof outcome === 'object' && 'data' in outcome) {
    if (outcome.data?.success) return 'ok';
    return isPermissionRefusalBody(outcome.data) ? 'forbidden' : 'error';
  }

  return 'error';
}

/**
 * True for any classifyLoad() result other than 'ok' — the shorthand every
 * consumer of this module actually wants: "did this call give us data we
 * can render", not which of the three failure shapes it was.
 *
 * @param {'ok'|'forbidden'|'unauthenticated'|'error'|null|undefined} status
 * @returns {boolean}
 */
export function isLoadFailed(status) {
  return Boolean(status) && status !== 'ok';
}
