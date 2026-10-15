/*
Copyright (C) 2023-2026 QuantumNous

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
// The browser-side session hints the sign-in flow leaves behind.
//
// The gin session cookie is the real credential. These localStorage entries
// are hints other code reads: `user` (lib/auth.ts clears it when a session
// expires, so a stale hint cannot bounce a signed-out visitor back into the
// console) and `tenant_slug` (display only; the server resolves the tenant
// from the session). Storage may be denied (private mode); the cookie still
// works, so every access here is best effort.

const USER_KEY = 'user'
const TENANT_SLUG_KEY = 'tenant_slug'

export function hasStoredUser(): boolean {
  try {
    return window.localStorage.getItem(USER_KEY) !== null
  } catch {
    return false
  }
}

export function storeUser(user: unknown): void {
  try {
    window.localStorage.setItem(USER_KEY, JSON.stringify(user))
  } catch {
    // Hint only.
  }
}

/**
 * Remember the slug the server resolved. An empty one means the server could
 * not name the tenant; overwriting a working value with a guess is what once
 * produced 404s on every console panel.
 */
export function storeTenantSlug(slug: unknown): void {
  if (typeof slug !== 'string' || slug === '') return
  try {
    window.localStorage.setItem(TENANT_SLUG_KEY, slug)
  } catch {
    // Hint only.
  }
}

export function clearTenantSlug(): void {
  try {
    window.localStorage.removeItem(TENANT_SLUG_KEY)
  } catch {
    // Hint only.
  }
}
