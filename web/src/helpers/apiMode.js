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
const TENANT_SLUG_KEY = 'tenant_slug';

// The one endpoint that can tell this browser which tenant it belongs to
// without a platform cookie: it authenticates with newhub's own session
// cookie and its path carries no slug of its own (handler/oauth.go,
// GetSessionInfo). Every other source of the slug is a login response body,
// which is read once and then only lives in localStorage.
const SESSION_INFO_PATH = '/api/v2/auth/session-info';

// A resolution that never answers would hold the first paint forever, since
// the app start gates on it. Five seconds, then render without a slug and
// let the per-request repair in helpers/api.js pick it up.
const SESSION_INFO_TIMEOUT_MS = 5000;

export function isV2Mode() {
  return !!localStorage.getItem(TENANT_SLUG_KEY);
}

export function getTenantSlug() {
  return localStorage.getItem(TENANT_SLUG_KEY) || '';
}

export function setTenantSlug(slug) {
  if (slug) {
    localStorage.setItem(TENANT_SLUG_KEY, slug);
  }
}

export function clearTenantSlug() {
  localStorage.removeItem(TENANT_SLUG_KEY);
}

// Build V2 API path: /api/v2/{slug}{path}
//
// No deployment-specific fallback lives here any more. It used to read
// `getTenantSlug() || 'lurus'` — the slug of this one deployment's tenant,
// compiled into the frontend — while hooks/common/useTenantSlug.js fell back
// to 'default' for the same concept, so one browser window could produce
// both 200s and 404s for the same tenant. The only caller (helpers/token.js)
// gates on isV2Mode(), which is exactly "a slug is stored".
export function v2Url(path) {
  return `/api/v2/${getTenantSlug()}${path}`;
}

// Single-flight slug resolution. A page mount fires a dozen tenant-scoped
// reads at once; they must not each buy their own session-info round trip.
let slugInFlight = null;

// Deliberately window.fetch rather than the shared axios instance: that
// instance's interceptor treats a 401 as "re-establish the session", which
// is the loop this resolution exists to get out of, and importing it here
// would make helpers/api.js ↔ helpers/apiMode.js a cycle.
async function fetchSessionTenantSlug() {
  const base = import.meta.env?.VITE_REACT_APP_SERVER_URL || '';
  const controller =
    typeof AbortController === 'function' ? new AbortController() : null;
  const timer = controller
    ? setTimeout(() => controller.abort(), SESSION_INFO_TIMEOUT_MS)
    : null;
  try {
    const res = await fetch(`${base}${SESSION_INFO_PATH}`, {
      credentials: 'include',
      headers: { 'Cache-Control': 'no-store' },
      signal: controller ? controller.signal : undefined,
    });
    if (!res.ok) return '';
    const body = await res.json();
    const slug = body?.data?.tenant_slug || '';
    if (slug) {
      setTenantSlug(slug);
    }
    return slug;
  } catch (_) {
    // No session, offline, timed out — the caller renders/fails without a
    // slug rather than inventing one.
    return '';
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * Resolve this browser's tenant slug, storing it for the synchronous readers.
 *
 * @param {{force?: boolean}} [opts] force: re-ask even when a slug is stored
 *   — used by the 404 TENANT_NOT_FOUND repair, where the stored slug is
 *   precisely what the server just rejected.
 * @returns {Promise<string>} the slug, or '' when it could not be resolved.
 */
export function ensureTenantSlug({ force = false } = {}) {
  const stored = getTenantSlug();
  if (!force && stored) {
    return Promise.resolve(stored);
  }
  if (!slugInFlight) {
    slugInFlight = fetchSessionTenantSlug().finally(() => {
      slugInFlight = null;
    });
  }
  return slugInFlight;
}

/**
 * The app-start gate (src/index.jsx). The console reads the slug
 * synchronously everywhere (hooks/common/useTenantSlug), so the one moment
 * it can be fetched is before anything mounts — otherwise the first paint
 * fires a wave of tenant-scoped reads with no tenant to name.
 *
 * Only a browser that believes it is logged in has a slug to resolve: the
 * durable `user` shim is what the rest of the console treats as "signed in".
 * Never rejects — a failure here must not cost the first render.
 */
export async function prepareTenantSlug() {
  try {
    if (!localStorage.getItem('user')) {
      return '';
    }
    return await ensureTenantSlug();
  } catch (_) {
    return '';
  }
}
