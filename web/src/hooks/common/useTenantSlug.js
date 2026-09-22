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

import { SELF_TENANT_SLUG, getTenantSlug } from '../../helpers/apiMode';

/**
 * The :tenant_slug for this console's requests — always the self-tenant
 * alias, which the server resolves from the session
 * (helpers/apiMode.js SELF_TENANT_SLUG).
 *
 * It used to be the slug stored at login, with a literal fallback when none
 * was stored ('default', the tenant *id*, which no slug matches) — so one
 * missed write at login turned every panel into a 404 while the shell still
 * looked signed in (hub.lurus.cn, 2026-09-22). There is nothing left to
 * store, miss or fall back from.
 *
 * Still a hook (and still synchronous, and still constant for the life of
 * the page) so the pages that key effects on it need no change.
 */
export const useTenantSlug = () => SELF_TENANT_SLUG;

/**
 * The real slug the last login reported, for DISPLAY only ('' if none).
 * Never put it in a request path.
 */
export const readTenantSlug = getTenantSlug;

export default useTenantSlug;
