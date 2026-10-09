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
import { isCancelledError } from '@tanstack/react-query'
import { isCancel } from 'axios'
import i18next from 'i18next'

import { ApiError } from '@/lib/api-error'

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

/** Walk only error origins, never request configs or arbitrary response data. */
export function getServerErrorSources(value: unknown): Record<string, unknown>[] {
  const sources: Record<string, unknown>[] = []
  const pending = [value]
  const seen = new Set<object>()
  for (let index = 0; index < pending.length; index++) {
    const source = pending[index]
    if (!isRecord(source) || seen.has(source)) continue
    seen.add(source)
    sources.push(source)
    if (isRecord(source.response)) pending.push(source.response.data)
    pending.push(source.cause, source.error)
  }
  return sources
}

export function getServerErrorStatus(value: unknown): number | undefined {
  for (const source of getServerErrorSources(value)) {
    const status = isRecord(source.response)
      ? source.response.status
      : source.status
    if (typeof status === 'number') return status
  }
  return undefined
}

export function isServerErrorCancelled(value: unknown): boolean {
  return getServerErrorSources(value).some(
    (source) =>
      isCancel(source) ||
      isCancelledError(source) ||
      source.name === 'AbortError'
  )
}

function messageText(value: unknown): string | undefined {
  if (typeof value !== 'string' || !value.trim()) return undefined
  // A proxy's HTML error document is not an actionable API error message.
  if (/^\s*(?:<!doctype|<html[\s>])/i.test(value)) return undefined
  return value
}

/** The message to show a person for a failed request. */
export function getServerErrorMessage(
  value: unknown,
  fallback?: string
): string {
  if (value instanceof ApiError) {
    const own = messageText(value.message)
    if (own) return own
  }
  for (const source of getServerErrorSources(value)) {
    // Response bodies carry the server's own wording; raw axios/Error
    // messages ("Request failed with status code 500") are not for people.
    if (source instanceof Error) continue
    const message = messageText(source.message)
    if (message) return message
  }
  return fallback ?? i18next.t('Something went wrong!')
}
