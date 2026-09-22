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

import { useState } from 'react';

// What goes in the path when this browser has not been told its tenant.
//
// It used to be the literal 'default' — the *id* of the tenant both
// deployments seed, whose routing *slug* is 'lurus'. A browser that reached
// the console without a stored slug (the login wrote it only on the
// bootstrap's success path) therefore asked for /api/v2/default/... and
// TenantSlugGuard answered 404 TENANT_NOT_FOUND to every panel, while the
// shell still rendered as signed in. Live on hub.lurus.cn, 2026-09-22: 20
// such 404s in one browser window.
//
// This placeholder is not a guess at a tenant name: no row can carry it, so
// the 404 is immediate and helpers/api.js re-resolves the slug and replays
// the request (the repair keys on TENANT_NOT_FOUND). The empty string would
// NOT do — nginx merge_slashes collapses /api/v2//user/me into
// /api/v2/user/me, a different, platform-scoped route.
//
// In the normal case nothing is ever built from this: src/index.jsx resolves
// the slug before the first render (helpers/apiMode.js prepareTenantSlug).
export const UNRESOLVED_TENANT_SLUG = '-';

/** The stored tenant slug, read synchronously. */
export const readTenantSlug = () => {
  try {
    return localStorage.getItem('tenant_slug') || UNRESOLVED_TENANT_SLUG;
  } catch (_) {
    // Private mode denies localStorage entirely.
    return UNRESOLVED_TENANT_SLUG;
  }
};

/**
 * The current tenant slug, resolved before the first render.
 *
 * Ten v2 pages each carried their own copy of this, and every copy seeded
 * state with the literal 'default' and only read localStorage in an effect.
 * That makes the first render use a slug the browser was never given, so every
 * tenant-scoped fetch on mount fires twice: once against 'default' and again
 * against the real tenant. Where no tenant is named 'default' the first request
 * is answered 404 by TenantSlugGuard — observed on the v2 settings billing
 * panel, which requested /api/v2/default/billing/invoices and only then
 * /api/v2/lurus/billing/invoices.
 *
 * Reading in the initialiser costs nothing and removes the wasted round trip.
 * It is also complete: switching tenants writes the slug and then reloads the
 * page (HFShell's switchTenantSlug), so the value cannot change underneath a
 * mounted component and there is nothing for an effect to catch.
 */
export const useTenantSlug = () => useState(readTenantSlug)[0];

export default useTenantSlug;
