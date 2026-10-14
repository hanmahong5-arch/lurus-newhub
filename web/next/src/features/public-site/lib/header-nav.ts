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
/**
 * The operator's top-bar switches, from the `HeaderNavModules` option that
 * /api/status carries as a JSON string, e.g.
 * `{"home":true,"console":true,"pricing":{"enabled":true,"requireAuth":false},
 *   "docs":true,"about":true}`.
 *
 * Anything missing or unparsable means "enabled": a broken option must not
 * hide the price list. Legacy installs stored `pricing` as a bare boolean.
 */
export interface HeaderNav {
  pricing: boolean
  /** Sign-in is required to read the price list (UI gate; the API is open). */
  pricingRequiresAuth: boolean
  about: boolean
  docs: boolean
}

const DEFAULT_NAV: HeaderNav = {
  pricing: true,
  pricingRequiresAuth: false,
  about: true,
  docs: true,
}

function flag(value: unknown): boolean {
  return value !== false
}

export function parseHeaderNav(raw: unknown): HeaderNav {
  if (typeof raw !== 'string' || raw.trim() === '') return DEFAULT_NAV
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return DEFAULT_NAV
  }
  if (typeof parsed !== 'object' || parsed === null) return DEFAULT_NAV
  const modules = parsed as Record<string, unknown>
  const pricing = modules.pricing
  const pricingObject =
    typeof pricing === 'object' && pricing !== null
      ? (pricing as Record<string, unknown>)
      : null
  return {
    pricing: pricingObject ? flag(pricingObject.enabled) : flag(pricing),
    pricingRequiresAuth: pricingObject?.requireAuth === true,
    about: flag(modules.about),
    docs: flag(modules.docs),
  }
}
