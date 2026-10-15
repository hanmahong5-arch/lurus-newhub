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
import {
  DEFAULT_PLAN,
  NAME_MAX_LENGTH,
  PLAN_OPTIONS,
  SLUG_MAX_LENGTH,
} from '../constants'
import type { CreateTenantBody, UpdateTenantBody } from '../api'
import type { Tenant } from '../types'
import { parseMoneyText, type FromDisplay } from './pool'

/** Lower-case, and anything outside a-z 0-9 - becomes a dash. */
export function slugify(raw: string): string {
  return raw.toLowerCase().replaceAll(/[^a-z0-9-]/g, '-')
}

export type FieldError =
  | 'required'
  | 'too-long'
  | 'invalid-number'
  | 'invalid-amount'
  | 'currency'
  | 'cannot-clear'

// ─── Create ───────────────────────────────────────────────────────────────

export interface CreateFormValues {
  name: string
  slug: string
  orgId: string
  plan: string
  maxUsers: string
  /** Display money; blank = the platform default. */
  quotaCap: string
  rpm: string
  tpm: string
}

export const EMPTY_CREATE_FORM: CreateFormValues = {
  name: '',
  slug: '',
  orgId: '',
  plan: DEFAULT_PLAN,
  maxUsers: '',
  quotaCap: '',
  rpm: '',
  tpm: '',
}

export type CreateField = keyof CreateFormValues

export type CreateParse =
  | { ok: true; body: CreateTenantBody }
  | { ok: false; errors: Partial<Record<CreateField, FieldError>> }

function wholeNumber(raw: string): number | null {
  const text = raw.trim()
  if (!/^\d+$/.test(text)) return null
  const n = Number.parseInt(text, 10)
  return Number.isSafeInteger(n) ? n : null
}

/** Form -> POST body, or the per-field reasons it cannot be sent yet. */
export function parseCreateForm(
  values: CreateFormValues,
  fromDisplay: FromDisplay
): CreateParse {
  const errors: Partial<Record<CreateField, FieldError>> = {}
  const name = values.name.trim()
  const slug = values.slug.trim()
  const orgId = values.orgId.trim()

  if (!name) errors.name = 'required'
  else if (name.length > NAME_MAX_LENGTH) errors.name = 'too-long'
  if (!slug) errors.slug = 'required'
  else if (slug.length > SLUG_MAX_LENGTH) errors.slug = 'too-long'
  if (!orgId) errors.orgId = 'required'

  const body: CreateTenantBody = {
    zitadel_org_id: orgId,
    slug,
    name,
    plan_type: values.plan,
  }

  if (values.maxUsers.trim() !== '') {
    const seats = wholeNumber(values.maxUsers)
    if (seats === null || seats < 1) errors.maxUsers = 'invalid-number'
    else body.max_users = seats
  }

  if (values.quotaCap.trim() !== '') {
    const typed = parseMoneyText(values.quotaCap)
    if (typed === null) errors.quotaCap = 'invalid-amount'
    else {
      const quota = fromDisplay(typed)
      if (quota === null) errors.quotaCap = 'currency'
      else if (quota < 1) errors.quotaCap = 'invalid-amount'
      else body.max_quota = quota
    }
  }

  for (const [field, key] of [
    ['rpm', 'rate_limit_rpm'],
    ['tpm', 'rate_limit_tpm'],
  ] as const) {
    if (values[field].trim() === '') continue
    const n = wholeNumber(values[field])
    if (n === null) errors[field] = 'invalid-number'
    else if (n > 0) body[key] = n
  }

  return Object.keys(errors).length > 0 ? { ok: false, errors } : { ok: true, body }
}

// ─── Edit ─────────────────────────────────────────────────────────────────

export interface EditFormValues {
  name: string
  plan: string
  maxUsers: string
  quotaCap: string
  walletAuthoritative: boolean
}

/** The form as the tenant stands today. `toDisplay` maps quota -> display money. */
export function editFormFromTenant(
  tenant: Tenant,
  toDisplay: (quota: number) => number | null
): EditFormValues {
  const cap = tenant.max_quota > 0 ? toDisplay(tenant.max_quota) : null
  return {
    name: tenant.name,
    plan: tenant.plan_type || DEFAULT_PLAN,
    maxUsers: tenant.max_users > 0 ? String(tenant.max_users) : '',
    quotaCap: cap === null ? '' : String(cap),
    walletAuthoritative: tenant.wallet_authoritative === true,
  }
}

export type EditField = keyof EditFormValues

export type EditParse =
  | { ok: true; patch: UpdateTenantBody }
  | { ok: false; errors: Partial<Record<EditField, FieldError>> }

/**
 * Form -> PUT body carrying only what changed. The server ignores a zero or
 * blank name, plan, seat count and quota cap, so those cannot be cleared here:
 * a cleared cap is refused instead of being silently left as it was.
 */
export function parseEditForm(
  values: EditFormValues,
  initial: EditFormValues,
  fromDisplay: FromDisplay
): EditParse {
  const errors: Partial<Record<EditField, FieldError>> = {}
  const patch: UpdateTenantBody = {}

  const name = values.name.trim()
  if (name !== initial.name) {
    if (!name) errors.name = 'required'
    else if (name.length > NAME_MAX_LENGTH) errors.name = 'too-long'
    else patch.name = name
  }

  if (values.plan !== initial.plan) {
    if (!values.plan) errors.plan = 'required'
    else patch.plan_type = values.plan
  }

  if (values.maxUsers.trim() !== initial.maxUsers.trim()) {
    const seats = wholeNumber(values.maxUsers)
    if (values.maxUsers.trim() === '') errors.maxUsers = 'cannot-clear'
    else if (seats === null || seats < 1) errors.maxUsers = 'invalid-number'
    else patch.max_users = seats
  }

  if (values.quotaCap.trim() !== initial.quotaCap.trim()) {
    if (values.quotaCap.trim() === '') errors.quotaCap = 'cannot-clear'
    else {
      const typed = parseMoneyText(values.quotaCap)
      if (typed === null) errors.quotaCap = 'invalid-amount'
      else {
        const quota = fromDisplay(typed)
        if (quota === null) errors.quotaCap = 'currency'
        else if (quota < 1) errors.quotaCap = 'invalid-amount'
        else patch.max_quota = quota
      }
    }
  }

  if (values.walletAuthoritative !== initial.walletAuthoritative) {
    patch.wallet_authoritative = values.walletAuthoritative
  }

  return Object.keys(errors).length > 0 ? { ok: false, errors } : { ok: true, patch }
}

/** Plans to offer: the platform's, plus the tenant's own if it is outside them. */
export function planChoices(current?: string): string[] {
  const known: string[] = [...PLAN_OPTIONS]
  return current && !known.includes(current) ? [...known, current] : known
}

// ─── Rate limits ──────────────────────────────────────────────────────────

/** A blank field is 0 (unlimited); anything else is a whole number >= 0. */
export function parseLimit(raw: string): number {
  return Math.max(0, Number.parseInt(raw, 10) || 0)
}

/** The prefill of a limit field: 0 (unlimited) is shown as empty. */
export function limitFieldValue(limit: number | undefined): string {
  return limit !== undefined && limit > 0 ? String(limit) : ''
}
