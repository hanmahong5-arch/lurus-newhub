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
import type { MoneyConfig } from '@/lib/money'

import type { ApiToken, TokenWriteBody } from '../types'
import { parseCapInput, quotaToDisplayInput } from './quota'

export interface KeyFormValues {
  name: string
  unlimited: boolean
  /** Total cap in display currency (used + remaining). */
  cap: string
  limitModels: boolean
  models: string
  allowIps: string
  projectId: string
  /** yyyy-mm-dd, '' = never expires. */
  expiry: string
}

export const EMPTY_FORM: KeyFormValues = {
  name: '',
  unlimited: true,
  cap: '',
  limitModels: false,
  models: '',
  allowIps: '',
  projectId: '',
  expiry: '',
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

export function unixToDateInput(sec: number): string {
  if (!(sec > 0)) return ''
  const d = new Date(sec * 1000)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** End of the chosen local day, so "expires today" still works all day. */
export function dateInputToUnix(value: string): number {
  if (!value) return -1
  const ms = new Date(`${value}T23:59:59`).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : Number.NaN
}

export function formFromToken(
  token: ApiToken,
  config: MoneyConfig | null
): KeyFormValues {
  return {
    name: token.name,
    unlimited: token.unlimited_quota,
    cap: token.unlimited_quota
      ? ''
      : quotaToDisplayInput(token.used_quota + token.remain_quota, config),
    limitModels: token.model_limits_enabled,
    models: token.model_limits,
    allowIps: token.allow_ips,
    projectId: token.project_id > 0 ? String(token.project_id) : '',
    expiry: unixToDateInput(token.expired_time),
  }
}

export type FormError =
  | 'name'
  | 'cap-unavailable'
  | 'cap-invalid'
  | 'expiry'
  | 'models'

/**
 * Form -> request body. `usedQuota` is the key's current spend (0 on create):
 * the cap field is the TOTAL, the server stores the REMAINING.
 */
export function buildWriteBody(
  form: KeyFormValues,
  config: MoneyConfig | null,
  usedQuota: number
): { ok: true; body: TokenWriteBody } | { ok: false; error: FormError } {
  const name = form.name.trim()
  if (name === '') return { ok: false, error: 'name' }

  let remain = 0
  if (!form.unlimited) {
    const cap = parseCapInput(form.cap, config)
    if (!cap.ok) {
      return {
        ok: false,
        error: cap.reason === 'unavailable' ? 'cap-unavailable' : 'cap-invalid',
      }
    }
    remain = Math.max(0, cap.quota - usedQuota)
  }

  const expired = dateInputToUnix(form.expiry)
  if (Number.isNaN(expired)) return { ok: false, error: 'expiry' }

  const models = form.models.trim()
  if (form.limitModels && models === '') return { ok: false, error: 'models' }

  return {
    ok: true,
    body: {
      name,
      expired_time: expired,
      unlimited_quota: form.unlimited,
      remain_quota: remain,
      model_limits_enabled: form.limitModels,
      model_limits: form.limitModels ? models : '',
      allow_ips: form.allowIps.trim(),
      project_id: Number.parseInt(form.projectId, 10) || 0,
    },
  }
}
