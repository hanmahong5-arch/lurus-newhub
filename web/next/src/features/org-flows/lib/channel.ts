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

import { ApiError } from '@/lib/api'

import { MISSING_CHANNEL_ID, type ChannelCreateBody } from '../types'
import {
  AZURE_TYPE,
  NO_DEFAULT_BASE_URL_TYPES,
  VERTEX_TYPE,
  presetBaseUrl,
} from './vendors'

/**
 * Held in plain component state, NEVER persisted: it carries a live upstream
 * provider key, and writing that to localStorage would be a new exposure.
 */
export interface ChannelForm {
  type: number
  name: string
  baseURL: string
  key: string
  models: string
  orgId: string
  /** Region document (Vertex) / API version (Azure). */
  other: string
}

export const CHANNEL_FORM_INIT: ChannelForm = {
  type: 1,
  name: '',
  baseURL: '',
  key: '',
  models: '',
  orgId: '',
  other: '',
}

/** Comma list -> trimmed, non-empty, de-duplicated entries (order kept). */
export function splitModels(text: string): string[] {
  const seen = new Set<string>()
  for (const part of text.split(',')) {
    const m = part.trim()
    if (m !== '') seen.add(m)
  }
  return [...seen]
}

/** Existing model list plus the newly selected ones, never a blind overwrite. */
export function mergeModels(current: string, added: Iterable<string>): string {
  const set = new Set(splitModels(current))
  for (const m of added) set.add(m)
  return [...set].join(',')
}

/** True when the type must be given an explicit base url. */
export function needsBaseUrl(type: number): boolean {
  return NO_DEFAULT_BASE_URL_TYPES.has(type)
}

/** Which `other` input (if any) the vendor needs. */
export function otherKind(type: number): 'region' | 'api-version' | null {
  if (type === VERTEX_TYPE) return 'region'
  if (type === AZURE_TYPE) return 'api-version'
  return null
}

/**
 * New base url after a vendor card is picked. A value the person typed
 * themselves always survives; a preset-derived value is replaced by the new
 * vendor's own preset (a plain `current || preset` would leave the previous
 * vendor's host on a Zhipu channel).
 */
export function baseUrlAfterVendorPick(
  type: number,
  current: string,
  touched: boolean
): string {
  return touched ? current : presetBaseUrl(type)
}

export type ChannelFormIssue =
  | 'multi_key'
  | 'base_url_required'
  | 'other_required'
  | 'other_invalid'

/**
 * Checks `.trim()` cannot make: an internal newline survives it (a pasted
 * multi-key blob), and this wizard sends no channel_info, so the backend would
 * store the whole blob as one unusable literal key.
 */
export function validateChannelForm(
  form: ChannelForm
): ChannelFormIssue | null {
  if (/[\r\n]/.test(form.key.trim())) return 'multi_key'
  const type = Number(form.type) || 1
  if (needsBaseUrl(type) && form.baseURL.trim() === '') {
    return 'base_url_required'
  }
  if (type === VERTEX_TYPE) {
    const other = form.other.trim()
    if (other === '') return 'other_required'
    try {
      const parsed: unknown = JSON.parse(other)
      const ok =
        typeof parsed === 'object' &&
        parsed !== null &&
        !Array.isArray(parsed) &&
        (parsed as Record<string, unknown>).default != null
      if (!ok) return 'other_invalid'
    } catch {
      return 'other_invalid'
    }
  }
  return null
}

/** Whether the create button may fire at all (the three required fields). */
export function canCreateChannel(form: ChannelForm): boolean {
  return (
    form.name.trim() !== '' &&
    form.key.trim() !== '' &&
    form.models.trim() !== ''
  )
}

/** Exactly the body CreateChannelV2 parses. */
export function buildChannelBody(form: ChannelForm): ChannelCreateBody {
  const body: ChannelCreateBody = {
    name: form.name.trim(),
    type: Number(form.type) || 1,
    base_url: form.baseURL.trim(),
    key: form.key.trim(),
    models: form.models.trim(),
  }
  if (form.orgId.trim() !== '') body.openai_organization = form.orgId.trim()
  if (otherKind(body.type) !== null && form.other.trim() !== '') {
    body.other = form.other.trim()
  }
  return body
}

/**
 * Turn a failed channel call into something a tenant admin can act on. A
 * channel create always sets `key`, which the channel:sensitive_write gate
 * covers, so an ordinary tenant admin without that grant hits a bare
 * "insufficient permission" 403 on every create: name the missing grant.
 */
export function describeChannelError(error: unknown, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === 403 &&
    error.code === 'PERMISSION_DENIED'
  ) {
    return t(
      'Missing the channel:sensitive_write grant. Creating a channel sets its key, so it needs this grant even for an otherwise ordinary tenant admin. Ask a root admin to grant channel:sensitive_write, or have them create the channel for you.'
    )
  }
  if (error instanceof ApiError && error.code === MISSING_CHANNEL_ID) {
    return t('The server did not return the new channel id')
  }
  const message =
    error instanceof Error && error.message.trim() !== '' ? error.message : ''
  return message || t('Failed to reach the channel API')
}

export function validationMessage(
  issue: ChannelFormIssue,
  t: TFunction
): string {
  switch (issue) {
    case 'multi_key':
      return t(
        'One key per channel. Remove the extra line(s); multi-key channels are created from the Channels page.'
      )
    case 'base_url_required':
      return t(
        'This vendor has no default upstream host. Enter a base URL before creating the channel.'
      )
    case 'other_required':
      return t(
        'Google Vertex needs a deployment region. Enter it as JSON, for example {"default": "us-central1"}.'
      )
    case 'other_invalid':
      return t(
        'The deployment region must be a JSON object with a "default" entry, for example {"default": "us-central1"}.'
      )
  }
}
