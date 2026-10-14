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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { COUNT_MAX, COUNT_MIN, NAME_MAX_BYTES } from '../constants'
import type { CreateRedemptionBody, Redemption } from '../types'
import { byteLength } from './utils'

export function getRedemptionFormSchema(t: TFunction) {
  const countMessage = t('Count must be between {{min}} and {{max}}', {
    min: COUNT_MIN,
    max: COUNT_MAX,
  })
  return z.object({
    name: z
      .string()
      .refine((v) => v.trim().length > 0, t('Enter a name'))
      .refine(
        (v) => byteLength(v.trim()) <= NAME_MAX_BYTES,
        t('Name can be at most {{max}} bytes', { max: NAME_MAX_BYTES })
      ),
    quota: z
      .string()
      .regex(/^\d+(\.\d+)?$/, t('Enter a valid amount'))
      .refine((v) => Number.parseFloat(v) > 0, t('Quota must be above zero')),
    expired_time: z.date().optional(),
    count: z
      .number({ error: t('Enter a whole number') })
      .int(t('Enter a whole number'))
      .min(COUNT_MIN, countMessage)
      .max(COUNT_MAX, countMessage),
  })
}

export type RedemptionFormValues = z.infer<
  ReturnType<typeof getRedemptionFormSchema>
>

export const REDEMPTION_FORM_DEFAULTS: RedemptionFormValues = {
  name: '',
  quota: '10',
  expired_time: undefined,
  count: 1,
}

/** Date picked in the form -> unix seconds (0 = never). */
export function dateToExpiry(date: Date | undefined): number {
  return date ? Math.floor(date.getTime() / 1000) : 0
}

export function expiryToDate(expired: number): Date | undefined {
  return expired > 0 ? new Date(expired * 1000) : undefined
}

export function redemptionToFormValues(
  redemption: Redemption,
  toDisplayAmount: (quota: number) => number | null
): RedemptionFormValues {
  const amount = toDisplayAmount(redemption.quota)
  return {
    name: redemption.name,
    quota: amount === null ? '' : String(amount),
    expired_time: expiryToDate(redemption.expired_time),
    count: 1,
  }
}

export type PayloadResult =
  | { ok: true; body: CreateRedemptionBody }
  | { ok: false }

/**
 * Form values -> API body. The typed amount is display money; it becomes
 * quota only through the operator's currency settings, and when those are
 * not loaded nothing is guessed.
 */
export function formValuesToBody(
  values: RedemptionFormValues,
  fromDisplayAmount: (amount: number) => number | null
): PayloadResult {
  const quota = fromDisplayAmount(Number.parseFloat(values.quota))
  if (quota === null || quota < 1) return { ok: false }
  return {
    ok: true,
    body: {
      name: values.name.trim(),
      quota,
      count: values.count,
      expired_time: dateToExpiry(values.expired_time),
    },
  }
}
