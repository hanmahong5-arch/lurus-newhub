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
import type { KeySettingsBody } from '../api'

export const DEFAULT_KEY_WEIGHT = 50
export const MAX_KEY_WEIGHT = 100

export interface KeyForm {
  weight: string
  proxy: string
}

export type KeyFormError = 'weight'

export type KeyFormBuild =
  | { ok: true; body: KeySettingsBody }
  | { ok: false; error: KeyFormError }

/** Only fields that differ from the current values are sent. */
export function buildKeySettings(
  form: KeyForm,
  current: { weight: number; proxy: string }
): KeyFormBuild {
  const body: KeySettingsBody = {}
  const w = form.weight.trim()
  if (w !== '') {
    const n = Number(w)
    if (!Number.isInteger(n) || n < 0 || n > MAX_KEY_WEIGHT) {
      return { ok: false, error: 'weight' }
    }
    if (n !== current.weight) body.weight = n
  }
  const proxy = form.proxy.trim()
  if (proxy !== current.proxy) body.proxy = proxy
  return { ok: true, body }
}
