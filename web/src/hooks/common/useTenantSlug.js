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

// The slug a browser falls back to when it has never been told otherwise.
// Not every deployment has a tenant by this name.
export const DEFAULT_TENANT_SLUG = 'default';

/** The stored tenant slug, read synchronously. */
export const readTenantSlug = () => {
  try {
    return localStorage.getItem('tenant_slug') || DEFAULT_TENANT_SLUG;
  } catch (_) {
    // Private mode denies localStorage entirely.
    return DEFAULT_TENANT_SLUG;
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
