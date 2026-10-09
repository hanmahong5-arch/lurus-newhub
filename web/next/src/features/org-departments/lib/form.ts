import type { MoneyConfig } from '@/lib/money'

import type { Department, DepartmentWriteBody } from '../types'

export const EXTERNAL_CODE_MAX = 64
export const NAME_MAX = 128
export const DESCRIPTION_MAX = 512

export interface DepartmentFormValues {
  name: string
  description: string
  externalCode: string
  /** Budget in display currency; '' = no budget. */
  budget: string
}

export const EMPTY_FORM: DepartmentFormValues = {
  name: '',
  description: '',
  externalCode: '',
  budget: '',
}

export type FormError =
  | 'name'
  | 'name-long'
  | 'description-long'
  | 'code-format'
  | 'budget-invalid'
  | 'budget-unavailable'

/** Display-currency amount -> internal quota units (rate-aware), integer. */
export function displayToQuota(value: number, config: MoneyConfig): number {
  if (config.displayType === 'TOKENS') return Math.round(value)
  return Math.round((value / config.rate) * config.quotaPerUnit)
}

/** Internal quota -> editable display-currency string (no symbol). */
export function quotaToDisplayInput(
  quota: number,
  config: MoneyConfig | null
): string {
  if (config === null || !(quota > 0)) return ''
  if (config.displayType === 'TOKENS') return String(Math.round(quota))
  const value = (quota / config.quotaPerUnit) * config.rate
  return String(Number.parseFloat(value.toFixed(4)))
}

export function unitLabel(config: MoneyConfig | null): string {
  if (config === null) return ''
  return config.displayType === 'TOKENS' ? 'tokens' : config.symbol
}

/** Mirrors the server: '' is valid; else at most 64 UTF-8 bytes and no control characters. */
export function isValidExternalCode(raw: string): boolean {
  const code = raw.trim()
  if (code === '') return true
  if (new TextEncoder().encode(code).length > EXTERNAL_CODE_MAX) return false
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f-\u009f]/.test(code)
}

export function formFromDepartment(
  d: Department,
  config: MoneyConfig | null
): DepartmentFormValues {
  return {
    name: d.name,
    description: d.description,
    externalCode: d.externalCode,
    budget: quotaToDisplayInput(d.monthlyBudgetQuota, config),
  }
}

export type BuildResult =
  | { ok: true; body: DepartmentWriteBody }
  | { ok: false; error: FormError }

/**
 * Validate and convert. An empty budget (or 0) means "no budget" and is sent
 * as 0 so editing can also clear one. A typed budget with no currency config
 * is refused rather than guessed.
 */
export function buildWriteBody(
  form: DepartmentFormValues,
  config: MoneyConfig | null,
  originalQuota?: number
): BuildResult {
  const name = form.name.trim()
  if (name === '') return { ok: false, error: 'name' }
  if ([...name].length > NAME_MAX) return { ok: false, error: 'name-long' }
  if ([...form.description].length > DESCRIPTION_MAX) {
    return { ok: false, error: 'description-long' }
  }
  if (!isValidExternalCode(form.externalCode)) {
    return { ok: false, error: 'code-format' }
  }
  let quota = 0
  const rawBudget = form.budget.trim()
  if (rawBudget !== '') {
    if (config === null) return { ok: false, error: 'budget-unavailable' }
    if (!/^\d+(\.\d+)?$/.test(rawBudget)) {
      return { ok: false, error: 'budget-invalid' }
    }
    // The shown value is rounded to 4 decimals; if the user did not touch
    // it, resubmit the stored quota verbatim instead of a lossy round trip.
    quota =
      originalQuota !== undefined &&
      originalQuota > 0 &&
      rawBudget === quotaToDisplayInput(originalQuota, config)
        ? originalQuota
        : displayToQuota(Number.parseFloat(rawBudget), config)
  }
  return {
    ok: true,
    body: {
      name,
      description: form.description.trim(),
      monthly_budget_quota: quota,
      external_code: form.externalCode.trim(),
    },
  }
}

/** Start of the current calendar month (local time) as unix seconds. */
export function monthStartSec(now: Date = new Date()): number {
  return Math.floor(
    new Date(now.getFullYear(), now.getMonth(), 1).getTime() / 1000
  )
}

/** Used / budget as 0..1, null when there is no budget. */
export function budgetRatio(used: number, budget: number): number | null {
  return budget > 0 ? Math.min(1, used / budget) : null
}

/** Positive integer from the user-id field, or null. */
export function parseUserId(raw: string): number | null {
  const text = raw.trim()
  if (!/^\d+$/.test(text)) return null
  const n = Number.parseInt(text, 10)
  return Number.isSafeInteger(n) && n > 0 ? n : null
}
