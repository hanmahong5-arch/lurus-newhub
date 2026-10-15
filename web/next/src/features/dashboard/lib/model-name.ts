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
import { useTranslation } from 'react-i18next'

/**
 * Usage rows can carry no model name (traffic the relay could not attribute).
 * Data stays '' for them; only the label is localized, at render time, so a
 * real model that happens to be called "Unknown model" is never merged.
 */
export const BLANK_MODEL = ''

/** Missing, empty or whitespace-only. */
export function isBlankModel(name: string | null | undefined): boolean {
  return name == null || name.trim() === ''
}

/** The name as data: a blank one collapses to BLANK_MODEL. */
export function normalizeModel(name: string | null | undefined): string {
  return isBlankModel(name) ? BLANK_MODEL : (name as string)
}

/**
 * Stable partition: rows with a model name keep their order, rows without
 * one follow. The blank bucket is never ranked against real models.
 */
export function blankLast<T>(
  rows: readonly T[],
  nameOf: (row: T) => string | null | undefined
): T[] {
  const named: T[] = []
  const blank: T[] = []
  for (const r of rows) (isBlankModel(nameOf(r)) ? blank : named).push(r)
  return [...named, ...blank]
}

/** Localized label of a model name; a blank one reads "Unknown model". */
export function useModelLabel() {
  const { t } = useTranslation()
  return (name: string | null | undefined): string =>
    isBlankModel(name) ? t('Unknown model') : (name as string)
}
