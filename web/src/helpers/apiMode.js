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

// The :tenant_slug every tenant-scoped console request carries: "the tenant
// this session authenticated into". The server resolves it from the session
// (internal/adapter/middleware/tenant_scope.go, SelfTenantSlug — keep the two
// in step), so the console never has to know its own slug to reach its own
// data.
//
// It replaces a slug the browser stored at login. That stored value was the
// only way the console could name its tenant, and it went wrong three ways
// on the live deployment: a login that returned the tenant id instead of the
// slug (2026-09-10), a browser that never stored one and fell back to a
// literal (2026-09-22, every panel 404), and the root tenant switcher, which
// wrote another tenant's slug that the server then refused on every request
// (403 TENANT_MISMATCH — the session, not the URL, decides the tenant).
export const SELF_TENANT_SLUG = '~';

// "This browser signed in through the v2 console" — a login wrote a slug.
// The slug itself is no longer used for routing; see getTenantSlug.
export function isV2Mode() {
  return !!localStorage.getItem(TENANT_SLUG_KEY);
}

// The real slug the last login reported, for DISPLAY only (what to call this
// tenant on screen). Never build a request path from it — use v2Url or
// SELF_TENANT_SLUG. '' when no login has reported one.
export function getTenantSlug() {
  try {
    return localStorage.getItem(TENANT_SLUG_KEY) || '';
  } catch (_) {
    // Private mode denies localStorage entirely.
    return '';
  }
}

export function setTenantSlug(slug) {
  if (slug) {
    localStorage.setItem(TENANT_SLUG_KEY, slug);
  }
}

export function clearTenantSlug() {
  localStorage.removeItem(TENANT_SLUG_KEY);
}

// Build V2 API path: /api/v2/~{path}
export function v2Url(path) {
  return `/api/v2/${SELF_TENANT_SLUG}${path}`;
}
