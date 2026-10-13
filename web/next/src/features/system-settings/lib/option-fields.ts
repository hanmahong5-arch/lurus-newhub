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
import type { ReactNode } from 'react'

import type { OptionValue } from '../api'
import type { OptionMap } from '../hooks/use-system-options'

/**
 * Declarative description of one option the form engine edits.
 *
 * Every draft value is a string (switches use 'true' / 'false') so typing is
 * never fought by a parse/format round trip; the typed value is produced once,
 * by serializeField, when the form is saved.
 */
type FieldBase = {
  /** The option key exactly as the server stores it (e.g. legal.privacy_policy). */
  key: string
  /** English source string; translated at render time. */
  label: string
  description?: string
  /** Occupy both grid columns. */
  span?: 'full'
  /** Hide the field unless this returns true for the current draft. */
  visibleWhen?: (draft: DraftReader) => boolean
  disabledWhen?: (draft: DraftReader) => boolean
}

export type SwitchField = FieldBase & {
  kind: 'switch'
  defaultValue?: boolean
}

export type TextField = FieldBase & {
  kind: 'text'
  placeholder?: string
  /** Render a textarea with this many rows. */
  rows?: number
  required?: boolean
  /** Must be empty or an absolute http(s) URL. */
  url?: boolean
  /** Drop trailing slashes before saving (server addresses). */
  trimTrailingSlash?: boolean
  maxLength?: number
  defaultValue?: string
  validate?: (value: string) => string | null
}

export type NumberField = FieldBase & {
  kind: 'number'
  integer?: boolean
  min?: number
  max?: number
  /** Value must be strictly greater than zero. */
  positive?: boolean
  step?: number | string
  placeholder?: string
  defaultValue?: string
  /** Extra text under the control, derived from the parsed value. */
  hint?: (value: number | null) => ReactNode
}

export type SelectField = FieldBase & {
  kind: 'select'
  options: Array<{ value: string; label: string }>
  defaultValue?: string
}

export type JsonField = FieldBase & {
  kind: 'json'
  /** The top-level JSON type the server accepts for this key. */
  expect: 'object' | 'array'
  /** Extra shape check; return an English error string or null. */
  validate?: (parsed: unknown) => string | null
  heightClassName?: string
  placeholder?: string
  /** Visual editor offered next to the raw JSON editor. */
  editor?: (props: {
    value: string
    onChange: (value: string) => void
    disabled: boolean
  }) => ReactNode
}

/**
 * A JSON array of strings stored under one key, edited as one item per line
 * (or comma separated). Blank items are dropped.
 */
export type ListField = FieldBase & {
  kind: 'list'
  separator?: 'newline' | 'comma'
  placeholder?: string
  rows?: number
  validateItem?: (item: string) => string | null
}

/** Write-only: the server never returns the stored value. Blank = keep. */
export type SecretField = FieldBase & {
  kind: 'secret'
  placeholder?: string
}

export type OptionField =
  | SwitchField
  | TextField
  | NumberField
  | SelectField
  | JsonField
  | ListField
  | SecretField

export type DraftReader = {
  bool: (key: string) => boolean
  str: (key: string) => string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** Pretty JSON when raw parses, otherwise raw untouched. */
export function prettyJson(raw: string, empty: string): string {
  const text = raw.trim()
  if (text === '') return empty
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return raw
  }
}

function listItems(value: string, separator: 'newline' | 'comma'): string[] {
  return value
    .split(separator === 'comma' ? /[,\n]/ : /\n/)
    .map((item) => item.trim())
    .filter((item) => item !== '')
}

function listFromStored(raw: string, separator: 'newline' | 'comma'): string {
  const text = raw.trim()
  if (text === '' || text === 'null') return ''
  try {
    const parsed: unknown = JSON.parse(text)
    if (Array.isArray(parsed)) {
      return parsed.map((item) => String(item)).join(
        separator === 'comma' ? ',' : '\n'
      )
    }
  } catch {
    // fall through: show what is stored
  }
  return raw
}

function emptyJson(field: JsonField): string {
  return field.expect === 'array' ? '[]' : '{}'
}

/** What the form shows before the person touches a field. */
export function baselineOf(field: OptionField, map: OptionMap): string {
  const raw = map[field.key]
  switch (field.kind) {
    case 'switch': {
      if (raw === undefined) return field.defaultValue ? 'true' : 'false'
      return raw === 'true' || raw === '1' ? 'true' : 'false'
    }
    case 'json':
      return prettyJson(raw ?? '', emptyJson(field))
    case 'secret':
      return ''
    case 'list':
      return listFromStored(raw ?? '', field.separator ?? 'newline')
    case 'select':
    case 'text':
    case 'number':
      return raw ?? field.defaultValue ?? ''
  }
}

/** Canonical form used to decide whether a field changed. */
export function comparable(field: OptionField, value: string): string {
  switch (field.kind) {
    case 'switch':
      return value === 'true' ? 'true' : 'false'
    case 'json': {
      const text = value.trim()
      if (text === '') return emptyJson(field)
      try {
        return JSON.stringify(JSON.parse(text))
      } catch {
        return text
      }
    }
    case 'number': {
      const text = value.trim()
      if (text === '') return ''
      const n = Number(text)
      return Number.isFinite(n) ? String(n) : text
    }
    case 'text':
      return field.trimTrailingSlash ? value.replace(/\/+$/, '') : value
    case 'list':
      return listItems(value, field.separator ?? 'newline').join('\n')
    case 'secret':
      return value
    case 'select':
      return value
  }
}

export function isFieldDirty(
  field: OptionField,
  draft: string | undefined,
  baseline: string
): boolean {
  if (draft === undefined) return false
  if (field.kind === 'secret') return draft !== ''
  return comparable(field, draft) !== comparable(field, baseline)
}

function isHttpUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * Client-side check before anything is sent. Returns an English message (the
 * caller translates it) or null. The server re-validates; this only spares a
 * round trip for the mistakes people actually make.
 */
export function validateField(
  field: OptionField,
  value: string
): string | null {
  switch (field.kind) {
    case 'switch':
    case 'select':
    case 'secret':
      return null
    case 'list': {
      if (!field.validateItem) return null
      for (const item of listItems(value, field.separator ?? 'newline')) {
        const problem = field.validateItem(item)
        if (problem) return problem
      }
      return null
    }
    case 'text': {
      const text = value.trim()
      if (field.required && text === '') return 'This field is required'
      if (field.url && text !== '' && !isHttpUrl(text)) {
        return 'Enter an absolute http(s) URL'
      }
      if (field.maxLength !== undefined && value.length > field.maxLength) {
        return 'Value is too long'
      }
      return field.validate ? field.validate(value) : null
    }
    case 'number': {
      const text = value.trim()
      if (text === '') return 'Enter a number'
      const n = Number(text)
      if (!Number.isFinite(n)) return 'Please enter a valid number'
      if (field.integer && !Number.isInteger(n)) return 'Enter a whole number'
      if (field.positive && n <= 0) return 'Must be greater than 0'
      if (field.min !== undefined && n < field.min) {
        return 'Value is below the minimum'
      }
      if (field.max !== undefined && n > field.max) {
        return 'Value is above the maximum'
      }
      return null
    }
    case 'json': {
      const text = value.trim()
      if (text === '') return null
      let parsed: unknown
      try {
        parsed = JSON.parse(text)
      } catch {
        return 'Invalid JSON'
      }
      if (field.expect === 'object' && !isRecord(parsed)) {
        return 'Expected a JSON object'
      }
      if (field.expect === 'array' && !Array.isArray(parsed)) {
        return 'Expected a JSON array'
      }
      return field.validate ? field.validate(parsed) : null
    }
  }
}

/** The typed value PUT /api/option/ receives for a validated draft. */
export function serializeField(field: OptionField, value: string): OptionValue {
  switch (field.kind) {
    case 'switch':
      return value === 'true'
    case 'number':
      // Sent as a string: the server stringifies a JSON number with %f
      // ("5" would arrive as "5.000000") and its integer parser refuses that.
      return String(Number(value.trim()))
    case 'json': {
      const text = value.trim()
      return text === '' ? emptyJson(field) : JSON.stringify(JSON.parse(text))
    }
    case 'text':
      return field.trimTrailingSlash ? value.replace(/\/+$/, '') : value
    case 'list':
      return JSON.stringify(listItems(value, field.separator ?? 'newline'))
    case 'secret':
    case 'select':
      return value
  }
}
